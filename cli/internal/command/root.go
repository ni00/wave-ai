// Package command implements the CLI independently of process-global stdio.
package command

import (
	"context"
	"io"
	"sort"
	"strings"
	"time"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

type settings struct {
	baseURL, profile, format, selection string
	timeout                             time.Duration
	raw, pretty                         bool
}

func Execute(ctx context.Context, version string, args []string, in io.Reader, out, diagnostics io.Writer) int {
	root := newRoot(version)
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(diagnostics)
	if err := root.ExecuteContext(ctx); err != nil {
		return reportError(diagnostics, err)
	}
	return 0
}

func newRoot(version string) *cobra.Command {
	s := &settings{}
	root := &cobra.Command{
		Use: "wavectl", Short: "Submit, follow and manage Wave AI work", Version: version,
		Long:         "Wave AI HTTP client. Use resource commands for API operations and task/tool commands for execution workflows.\nConfiguration and schema commands work without a running service.",
		Example:      "  wavectl agents create --name assistant --model your-model\n  wavectl tasks create --session SESSION --agent AGENT --input-file prompt.txt --wait\n  wavectl tasks get TASK --format table\n  wavectl schema tasks create",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usage("%v", err) })
	flags := root.PersistentFlags()
	flags.StringVar(&s.baseURL, "url", "", "Wave API base URL; overrides WAVE_BASE_URL and the profile")
	flags.StringVar(&s.profile, "profile", "", "named connection; defaults to WAVE_PROFILE or the active profile")
	flags.StringVar(&s.format, "format", "json", "output format: json, jsonl, table")
	flags.BoolVar(&s.pretty, "pretty", false, "indent JSON output")
	flags.StringVar(&s.selection, "select", "", "select a result using JSON Pointer, e.g. /id or /task/id")
	flags.BoolVar(&s.raw, "raw", false, "print a selected string/number/boolean without JSON quoting")
	flags.DurationVar(&s.timeout, "timeout", 30*time.Second, "deadline; 0 disables; watch defaults to 0, wait to 5m")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return s.validateOutput() }
	root.AddCommand(schemaCommand(version), configCommand(s), doctorCommand(s))
	groups := addOperations(root, s)
	groups["tasks"].AddCommand(waitCommand(s))
	groups["tools"].AddCommand(approvalCommand(s, true), approvalCommand(s, false), resultCommand(s))
	normalizeArgs(root)
	return root
}

var resourceDescriptions = map[string]string{
	"agents": "Agent configuration and immutable versions", "sessions": "Sessions, history and pending user actions",
	"tasks": "Submit, inspect, steer and wait for tasks", "tools": "Inspect tool calls and commit explicit decisions/results",
	"events": "Read or stream durable session events", "files": "Upload, find and download files and artifacts",
	"skills": "Upload and retrieve Wave sandbox skill packages", "credentials": "Manage stored outbound credentials",
	"environments": "Execution environment configuration", "deployments": "Scheduled deployments and runs",
	"memory": "Memory stores, entries, revisions and conflicts",
}

func addOperations(root *cobra.Command, s *settings) map[string]*cobra.Command {
	groups := map[string]*cobra.Command{}
	operations := wave.Operations()
	ids := make([]string, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		op := operations[id]
		words := strings.Fields(op.Command)
		parent := root
		for _, name := range words[:len(words)-1] {
			if groups[name] == nil {
				groups[name] = &cobra.Command{Use: name, Short: resourceDescriptions[name]}
				parent.AddCommand(groups[name])
			}
			parent = groups[name]
		}
		parent.AddCommand(operationCommand(s, op, words[len(words)-1]))
	}
	return groups
}

func normalizeArgs(cmd *cobra.Command) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(command *cobra.Command, args []string) error {
			if err := validate(command, args); err != nil {
				return usage("%v", err)
			}
			return nil
		}
	}
	for _, child := range cmd.Commands() {
		normalizeArgs(child)
	}
}

func (s *settings) context(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	deadline := s.timeout
	if !cmd.Flags().Changed("timeout") {
		wait, _ := cmd.Flags().GetBool("wait")
		if cmd.Name() == "watch" {
			deadline = 0
		}
		if cmd.Name() == "wait" || wait {
			deadline = 5 * time.Minute
		}
	}
	if deadline == 0 {
		return context.WithCancel(cmd.Context())
	}
	return context.WithTimeout(cmd.Context(), deadline)
}
