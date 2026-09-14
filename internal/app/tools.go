package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"wave-ai.local/wave/internal/adapters/mcpclient"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/vault"
	"wave-ai.local/wave/internal/platform/auth"
)

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func principal(s execution.Session) *auth.Principal {
	return &auth.Principal{OrgID: s.OrgID, PrincipalID: s.OwnerID, Scope: auth.ScopeAPI}
}
func (a *App) execute(ctx context.Context, s execution.Session, call execution.ToolCall) (execution.ToolResult, error) {
	var in map[string]any
	if e := json.Unmarshal([]byte(call.Arguments), &in); e != nil {
		return execution.ToolFailed("invalid_arguments", "invalid tool arguments"), nil
	}
	if call.Tool.Kind == "mcp" {
		bearer := ""
		if call.Tool.CredentialID != "" {
			token, e := vault.SessionBearer(ctx, a.DB, a.Box, principal(s), call.Tool.CredentialID, call.Tool.ServerURL, s.VaultIDs)
			if e != nil {
				return execution.ToolFailed("credential_unavailable", "credential unavailable"), nil
			}
			bearer = "Bearer " + token
		}
		client := a.MCP.Get(s.OrgID+"/"+s.OwnerID+"/"+s.ID, call.Tool.Name, call.Tool.ServerURL, bearer)
		name := call.Tool.RemoteName
		if name == "" {
			name = call.Tool.Name
		}
		res, e := client.Call(ctx, name, in)
		if e != nil {
			var rejected *mcpclient.RejectedError
			if errors.As(e, &rejected) {
				return execution.ToolFailed("mcp_rejected", e.Error()), nil
			}
			return execution.ToolResult{}, e
		}
		raw, e := json.Marshal(res)
		code := ""
		if res.IsError {
			code = "mcp_tool_failed"
		}
		return execution.ToolResult{Output: string(raw), IsError: res.IsError, ErrorCode: code}, e
	}
	if s.EnvironmentID == "" {
		return execution.ToolFailed("environment_required", "This tool requires an environment"), nil
	}
	str := func(k string) string { v, _ := in[k].(string); return v }
	p := str("path")
	if p == "" {
		p = "/workspace"
	}
	if !path.IsAbs(p) {
		p = path.Join("/workspace", p)
	}
	switch call.Tool.Name {
	case "read":
		b, e := a.Sandbox.ReadFile(ctx, s.ID, p, (1<<20)+1)
		if e != nil {
			return execution.ToolFailed("read_failed", e.Error()), nil
		}
		if len(b) > 1<<20 {
			return execution.ToolResult{Output: string(b[:1<<20]) + "\n[truncated at 1 MiB]"}, nil
		}
		return execution.ToolResult{Output: string(b)}, nil
	case "write":
		if p == "/workspace" {
			return execution.ToolFailed("invalid_arguments", "path required"), nil
		}
		if e := a.Sandbox.WriteFile(ctx, s.ID, p, []byte(str("content"))); e != nil {
			return execution.ToolResult{}, e
		}
		return execution.ToolResult{Output: "written"}, nil
	case "edit":
		old := str("old")
		if old == "" {
			return execution.ToolFailed("invalid_arguments", "old text required"), nil
		}
		b, e := a.Sandbox.ReadFile(ctx, s.ID, p, (1<<20)+1)
		if e != nil {
			return execution.ToolFailed("read_failed", e.Error()), nil
		}
		if len(b) > 1<<20 {
			return execution.ToolFailed("file_too_large", "Edit refused: file exceeds 1 MiB"), nil
		}
		if strings.Count(string(b), old) != 1 {
			return execution.ToolFailed("edit_conflict", "old text must match exactly once"), nil
		}
		if e = a.Sandbox.WriteFile(ctx, s.ID, p, []byte(strings.Replace(string(b), old, str("new"), 1))); e != nil {
			return execution.ToolResult{}, e
		}
		return execution.ToolResult{Output: "edited"}, nil
	case "ls":
		items, e := a.Sandbox.ListDir(ctx, s.ID, p)
		if e != nil {
			return execution.ToolFailed("list_failed", e.Error()), nil
		}
		return execution.ToolResult{Output: strings.Join(items, "\n")}, nil
	case "bash", "grep", "find":
		command := str("command")
		if call.Tool.Name == "grep" {
			command = "grep -R -n -- " + quote(str("pattern")) + " " + quote(p)
		}
		if call.Tool.Name == "find" {
			command = "find " + quote(p) + " -name " + quote(str("pattern"))
		}
		if command == "" {
			return execution.ToolFailed("invalid_arguments", "command required"), nil
		}
		res, e := a.Sandbox.Exec(ctx, s.ID, sandbox.ExecRequest{Command: command, Cwd: "/workspace", TimeoutSec: 120, RequestID: call.ID})
		if e != nil {
			return execution.ToolResult{}, e
		}
		raw, _ := json.Marshal(res)
		return execution.ToolResult{Output: string(raw), IsError: res.ExitCode != 0, ErrorCode: exitErrorCode(res.ExitCode)}, nil
	default:
		return execution.ToolFailed("unsupported_tool", fmt.Sprintf("unsupported tool %q", call.Tool.Name)), nil
	}
}

func exitErrorCode(code int) string {
	if code != 0 {
		return "command_failed"
	}
	return ""
}
