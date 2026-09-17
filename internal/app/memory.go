package app

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/platform/apierr"
)

// memoryTool accesses only stores explicitly bound to this session. It needs
// neither a sandbox nor a task-finalization hook: successful writes are durable.
func (a *App) memoryTool(ctx context.Context, s execution.Session, call execution.ToolCall) (execution.ToolResult, error) {
	var in struct {
		StoreID string  `json:"store_id"`
		Path    string  `json:"path"`
		Content *string `json:"content"`
		Version *int    `json:"version"`
		Offset  int     `json:"offset"`
	}
	d := json.NewDecoder(strings.NewReader(call.Arguments))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil || in.Offset < 0 {
		return execution.ToolFailed("invalid_arguments", "invalid memory arguments"), nil
	}
	if !slices.Contains(s.MemoryIDs, in.StoreID) {
		return execution.ToolFailed("memory_unavailable", "memory store is not bound to this session"), nil
	}
	if _, err := memory.Get(ctx, a.DB, principal(s), in.StoreID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return execution.ToolFailed("memory_unavailable", "memory store is unavailable"), nil
		}
		return execution.ToolResult{}, err
	}
	var out any
	var err error
	if call.Tool.Name == "memory_read" && in.Path == "" {
		// Discovery omits content so a large store cannot flood model context.
		rows := []struct {
			Path    string `json:"path"`
			Version int    `json:"version"`
			Deleted bool   `json:"deleted"`
		}{}
		err = a.DB.WithContext(ctx).Model(&memory.Entry{}).Select("path", "version", "deleted").Where(clause.Eq{Column: "store_id", Value: in.StoreID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "path"}}).Offset(in.Offset).Limit(101).Find(&rows).Error
		next := 0
		if len(rows) > 100 {
			rows = rows[:100]
			next = in.Offset + 100
		}
		out = map[string]any{"entries": rows, "next_offset": next}
	} else {
		if in.Path == "" || path.IsAbs(in.Path) || path.Clean(in.Path) != in.Path || in.Path == "." || in.Path == ".." || strings.HasPrefix(in.Path, "../") || strings.ContainsAny(in.Path, "\\\x00") {
			return execution.ToolFailed("invalid_arguments", "memory path must be a clean relative path"), nil
		}
		var entry memory.Entry
		switch call.Tool.Name {
		case "memory_read":
			err = a.DB.WithContext(ctx).Where(clause.And(clause.Eq{Column: "store_id", Value: in.StoreID}, clause.Eq{Column: "path", Value: in.Path})).Take(&entry).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				entry = memory.Entry{StoreID: in.StoreID, Path: in.Path, Deleted: true}
				err = nil // Version zero is the precondition for creating this path.
			}
			if len(entry.Content) > 1<<20 {
				return execution.ToolFailed("memory_too_large", "memory entry exceeds 1 MiB"), nil
			}
		case "memory_write", "memory_delete":
			if in.Version == nil || *in.Version < 0 || (call.Tool.Name == "memory_write" && (in.Content == nil || len(*in.Content) > 1<<20)) {
				return execution.ToolFailed("invalid_arguments", "version must be nonnegative; writes require content of at most 1 MiB"), nil
			}
			if call.Tool.Name == "memory_write" {
				entry, err = memory.Write(ctx, a.DB, principal(s), in.StoreID, in.Path, *in.Content, s.ID, *in.Version)
			} else {
				entry, err = memory.Delete(ctx, a.DB, principal(s), in.StoreID, in.Path, s.ID, *in.Version)
			}
		}
		out = entry
	}
	var domain *apierr.Error
	if errors.As(err, &domain) && domain.Status < 500 {
		code := "invalid_arguments"
		if domain.Status == 409 {
			code = "memory_conflict"
		}
		return execution.ToolFailed(code, domain.Message), nil
	}
	if err != nil {
		return execution.ToolResult{}, err
	}
	raw, err := json.Marshal(out)
	return execution.ToolResult{Output: string(raw)}, err
}
