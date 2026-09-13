package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

func waitCommand(s *settings) *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{Use: "wait TASK_ID", Short: "Wait for the selected task to finish or require a user action", Args: cobra.ExactArgs(1),
		Long:    "Wait for one task. The command returns exit 6 with the pending actions, or exit 7 for a partial/failed/canceled task. It never approves tools or submits results automatically.",
		Example: "  wavectl tasks wait TASK --timeout 5m\n  wavectl tasks wait TASK --format table",
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 {
				return usage("interval must be positive")
			}
			c, err := s.client()
			if err != nil {
				return err
			}
			ctx, cancel := s.context(cmd)
			defer cancel()
			result, err := c.Wait(ctx, args[0], interval)
			if err != nil {
				return waitError(args[0], err)
			}
			return printWait(cmd, result)
		}}
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "polling interval")
	return cmd
}
func waitCreated(cmd *cobra.Command, ctx context.Context, c *wave.Client, data json.RawMessage) error {
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &task); err != nil {
		return err
	}
	if task.ID == "" {
		return fmt.Errorf("created task response has no ID")
	}
	result, err := c.Wait(ctx, task.ID, time.Second)
	if err != nil {
		return waitError(task.ID, err)
	}
	return printWait(cmd, result)
}
func waitError(task string, err error) error {
	hint := fmt.Sprintf("The existing task is %s. Continue with: wavectl tasks wait %s", task, task)
	code := 1
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		code = 5
	}
	var api *wave.APIError
	if errors.As(err, &api) {
		code = 4
		if api.StatusCode == 401 || api.StatusCode == 403 {
			code = 3
		}
	}
	return &exitError{code: code, err: fmt.Errorf("waiting for task %s: %w", task, err), hint: hint}
}
func printWait(cmd *cobra.Command, result *wave.WaitResult) error {
	if err := emit(cmd, result); err != nil {
		return err
	}
	id, _ := result.Task["id"].(string)
	if result.Reason == "action" {
		return &exitError{code: 6, err: fmt.Errorf("task %s requires action", id), hint: "Inspect required_actions in the result. Commit only the user's decision or the actual tool result, then wait again."}
	}
	if result.Task["state"] != "succeeded" {
		return &exitError{code: 7, err: fmt.Errorf("task %s ended with state %v", id, result.Task["state"]), hint: "Inspect the task result and error before deciding whether to resume."}
	}
	return nil
}
func approvalCommand(s *settings, approve bool) *cobra.Command {
	name := "reject"
	if approve {
		name = "approve"
	}
	var task string
	cmd := &cobra.Command{Use: name + " CALL_ID", Short: "Commit an explicit tool " + name + " decision", Args: cobra.ExactArgs(1),
		Example: "  wavectl tools " + name + " CALL --task TASK", RunE: func(cmd *cobra.Command, args []string) error {
			if task == "" {
				return usage("--task is required")
			}
			c, err := s.client()
			if err != nil {
				return err
			}
			ctx, cancel := s.context(cmd)
			defer cancel()
			if err = c.Approve(ctx, task, args[0], approve); err != nil {
				return err
			}
			return emit(cmd, map[string]any{"task_id": task, "call_id": args[0], "approve": approve, "accepted": true})
		}}
	cmd.Flags().StringVar(&task, "task", "", "task owning the tool call (required)")
	return cmd
}
func resultCommand(s *settings) *cobra.Command {
	var task, file, text, code string
	var isError bool
	cmd := &cobra.Command{Use: "result CALL_ID", Short: "Submit a confirmed client-tool result, preserving empty content", Args: cobra.ExactArgs(1),
		Example: "  wavectl tools result CALL --task TASK --result-file result.txt\n  wavectl tools result CALL --task TASK --result ''",
		RunE: func(cmd *cobra.Command, args []string) error {
			if task == "" {
				return usage("--task is required")
			}
			if cmd.Flags().Changed("result-file") == cmd.Flags().Changed("result") {
				return usage("provide exactly one of --result-file or --result (empty text is valid)")
			}
			if cmd.Flags().Changed("result-file") {
				data, err := readInput(cmd, file)
				if err != nil {
					return err
				}
				text = string(data)
			}
			body := map[string]any{"result": text, "is_error": isError, "error_code": code}
			if err := validateBody(wave.Operations()["executionResolveToolResult"], body); err != nil {
				return err
			}
			c, err := s.client()
			if err != nil {
				return err
			}
			ctx, cancel := s.context(cmd)
			defer cancel()
			if err = c.SubmitResult(ctx, task, args[0], text, isError, code); err != nil {
				return err
			}
			return emit(cmd, map[string]any{"task_id": task, "call_id": args[0], "accepted": true})
		}}
	cmd.Flags().StringVar(&task, "task", "", "task owning the tool call (required)")
	cmd.Flags().StringVar(&file, "result-file", "", "UTF-8 result file, or - for stdin")
	cmd.Flags().StringVar(&text, "result", "", "literal result text; explicitly passing an empty string is valid")
	cmd.Flags().BoolVar(&isError, "is-error", false, "the actual result represents a failure")
	cmd.Flags().StringVar(&code, "error-code", "", "failure code; requires --is-error")
	return cmd
}
