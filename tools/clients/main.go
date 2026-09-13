// clients is the repository's client generation, validation and release tool.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const usage = `Usage: go run ./tools/clients COMMAND

  generate [--check]           Generate owned files, or check without writing
  check                       Validate metadata, contracts and documentation
  compat SPEC OPERATIONS      Check compatibility with a previous release
  test                        Run SDK and CLI suites against shared HTTP fixtures
  integration                 Run isolated PostgreSQL/S3 workflows
  package                     Build local release archives; never publish
  smoke                       Install and verify the release archives
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "clients:", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, usage)
		return err
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	checkOnly := false
	switch command {
	case "generate":
		fs.BoolVar(&checkOnly, "check", false, "compare generated files without writing")
	case "check", "compat", "test", "integration", "package", "smoke":
	default:
		return fmt.Errorf("unknown command %q\n%s", command, usage)
	}
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	expected := 0
	if command == "compat" {
		expected = 2
	}
	if fs.NArg() != expected {
		return fmt.Errorf("%s expects %d positional arguments", command, expected)
	}
	root, err := findRoot()
	if err != nil {
		return err
	}
	t := &tool{root: root, out: out, diagnostics: diagnostics}
	if err = readJSON(t.path("tools/clients/config.json"), &t.config); err != nil {
		return err
	}
	if err = t.config.validate(); err != nil {
		return err
	}
	switch command {
	case "generate":
		return t.generate(ctx, checkOnly)
	case "check":
		return t.check()
	case "compat":
		spec, err := filepath.Abs(fs.Arg(0))
		if err != nil {
			return err
		}
		operations, err := filepath.Abs(fs.Arg(1))
		if err != nil {
			return err
		}
		return t.compat(ctx, spec, operations)
	case "test":
		return t.test(ctx)
	case "integration":
		return t.integration(ctx)
	case "package":
		return t.packageRelease(ctx)
	case "smoke":
		return t.smoke(ctx)
	}
	return nil
}

func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "tools/clients/config.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("run within the Wave AI checkout")
		}
		dir = parent
	}
}
