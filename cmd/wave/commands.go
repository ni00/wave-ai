package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"wave-ai.local/wave/internal/app"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/config"
	"wave-ai.local/wave/internal/platform/database"
)

const helpText = `Wave AI local service and administration

Usage:
  wave serve [-role all|api|worker|scheduler]
  wave init-db
  wave bootstrap -org NAME [-user admin] [-key-label default] [-format human|json|key]
  wave config check

Use 'wave COMMAND --help' for flags. Configuration is read from WAVE_* environment variables.
init-db initializes a new database; it does not migrate or reset existing deployments.
Remote API access and resource management: wavectl --help
`

func runCommand(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	name := "serve"
	if len(args) > 0 {
		name, args = args[0], args[1:]
	}
	if name == "help" || name == "--help" || name == "-h" {
		_, err := fmt.Fprint(out, helpText)
		return err
	}
	var err error
	switch name {
	case "serve":
		err = serveCommand(ctx, args, diagnostics)
	case "init-db":
		err = initCommand(ctx, args, diagnostics)
	case "bootstrap":
		err = bootstrapCommand(ctx, args, out, diagnostics)
	case "config":
		err = configCommand(args, out, diagnostics)
	default:
		return fmt.Errorf("unknown command %q; use wave --help", name)
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}
func flags(name string, diagnostics io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	return fs
}
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s: unexpected arguments %v", fs.Name(), fs.Args())
	}
	return nil
}
func serveCommand(ctx context.Context, args []string, diagnostics io.Writer) error {
	fs := flags("serve", diagnostics)
	role := fs.String("role", "", "all|api|worker|scheduler; defaults to WAVE_ROLE")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *role != "" {
		switch *role {
		case "all", "api", "worker", "scheduler":
			cfg.Role = *role
		default:
			return fmt.Errorf("invalid role %q", *role)
		}
	}
	service, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer service.Close()
	return service.Run(ctx)
}
func initCommand(ctx context.Context, args []string, diagnostics io.Writer) error {
	fs := flags("init-db", diagnostics)
	fs.Usage = func() {
		fmt.Fprintln(diagnostics, "Usage: wave init-db\nInitialize a new database using WAVE_DATABASE_URL. This is not a migration command.")
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	return app.Init(ctx, config.DatabaseURL())
}
func bootstrapCommand(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	fs := flags("bootstrap", diagnostics)
	org := fs.String("org", "", "organization (required)")
	user := fs.String("user", "admin", "user")
	label := fs.String("key-label", "default", "key label")
	format := fs.String("format", "human", "human, json, or key (raw key for secure piping)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *org == "" {
		return fmt.Errorf("bootstrap requires -org")
	}
	if *format != "human" && *format != "json" && *format != "key" {
		return fmt.Errorf("format must be human, json or key")
	}
	db, err := database.Open(ctx, config.DatabaseURL())
	if err != nil {
		return err
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	key, err := auth.Bootstrap(ctx, db, *org, *user, *label, auth.ScopeAPI)
	if err != nil {
		return err
	}
	if *format == "json" {
		return json.NewEncoder(out).Encode(map[string]string{"api_key": key})
	}
	if *format == "human" {
		if _, err = fmt.Fprintln(out, "API key created (shown once):"); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(out, key)
	return err
}
func configCommand(args []string, out, diagnostics io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(out, "Usage: wave config check\nValidate environment configuration locally; no network requests or secrets are printed.")
		return err
	}
	if args[0] != "check" {
		return fmt.Errorf("usage: wave config check")
	}
	if err := parse(flags("config check", diagnostics), args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.StorageBackend == "s3" && len(cfg.MasterKey) != 32 {
		return fmt.Errorf("S3 storage requires a 32-byte WAVE_MASTER_KEY")
	}
	return json.NewEncoder(out).Encode(map[string]any{"valid": true, "role": cfg.Role, "http_addr": cfg.HTTPAddr, "storage_backend": cfg.StorageBackend, "sandbox_backend": cfg.SandboxBackend, "database_configured": cfg.DatabaseURL != "", "model_key_configured": cfg.ModelAPIKey != "", "master_key_configured": len(cfg.MasterKey) > 0})
}
