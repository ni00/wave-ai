package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	wave "github.com/ni00/wave-ai/sdks/go"
)

type exitError struct {
	code int
	err  error
	hint string
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }
func usage(format string, args ...any) error {
	return &exitError{code: 2, err: fmt.Errorf(format, args...), hint: "Use the command's --help, or wavectl schema <resource> <command>."}
}

func reportError(out io.Writer, err error) int {
	code, hint := 1, ""
	var exit *exitError
	var api *wave.APIError
	switch {
	case errors.As(err, &exit):
		code, hint = exit.code, exit.hint
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code, hint = 5, "The client stopped waiting. Check the existing task before submitting more work."
	case errors.As(err, &api):
		code = 4
		if api.StatusCode == 401 || api.StatusCode == 403 {
			code = 3
			hint = "Check the selected profile and Wave API key; model-provider keys cannot authenticate to Wave."
		}
		if api.StatusCode == 409 {
			hint = "Inspect the resource state and request_id. Do not change an idempotency key to bypass a conflict."
		}
	case strings.HasPrefix(err.Error(), "unknown command "):
		code, hint = 2, "Run wavectl --help to see available commands."
	}
	detail := map[string]any{"message": err.Error(), "exit_code": code}
	if hint != "" {
		detail["hint"] = hint
	}
	if errors.As(err, &api) {
		detail["status"] = api.StatusCode
		detail["type"] = api.Code
		detail["request_id"] = api.RequestID
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"error": detail})
	return code
}
