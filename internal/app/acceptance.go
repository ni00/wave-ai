package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
)

func (a *App) evaluate(ctx context.Context, s execution.Session, t execution.Task) *execution.Evaluation {
	if len(t.Snapshot.Acceptance) == 0 {
		return nil
	}
	out := &execution.Evaluation{Status: "passed", Checks: []execution.CheckResult{}, EvaluatedAt: time.Now().UTC()}
	for _, check := range t.Snapshot.Acceptance {
		result := execution.CheckResult{Kind: check.Kind, Path: check.Path, Status: "passed"}
		raw := []byte(t.Result)
		if check.Path != "" {
			var f files.File
			e := auth.Owned(a.DB.WithContext(ctx), principal(s)).Where(clause.And(clause.Eq{Column: "task_id", Value: t.ID}, clause.Eq{Column: "path", Value: check.Path}, clause.Eq{Column: "deleted", Value: false})).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).Take(&f).Error
			if errors.Is(e, gorm.ErrRecordNotFound) {
				result.Status = "failed"
				result.Error = "required artifact missing"
			} else if e != nil {
				result.Status = "error"
				result.Error = "artifact lookup failed"
			} else if check.Kind == "json" {
				raw, e = a.Blobs.ReadAll(ctx, blobstore.Scope{OrgID: s.OrgID, OwnerID: s.OwnerID}, f.BlobKey, 1<<20)
				if e != nil {
					result.Status = "error"
					result.Error = "cannot read artifact (limit: 1 MiB)"
				}
			}
		}
		if result.Status == "passed" && check.Kind == "json" {
			if e := checkJSON(raw, check); e != nil {
				result.Status = "failed"
				result.Error = e.Error()
			}
		}
		if result.Status == "error" {
			out.Status = "error"
		} else if result.Status == "failed" && out.Status == "passed" {
			out.Status = "failed"
		}
		out.Checks = append(out.Checks, result)
	}
	return out
}
func checkJSON(raw []byte, check agents.AcceptanceCheck) error {
	if len(raw) > 1<<20 {
		return fmt.Errorf("JSON exceeds 1 MiB")
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return fmt.Errorf("expected a JSON object")
	}
	for _, key := range check.Required {
		if _, ok := v[key]; !ok {
			return fmt.Errorf("missing field: %s", key)
		}
	}
	for key, want := range check.Types {
		value, ok := v[key]
		if !ok {
			return fmt.Errorf("missing field: %s", key)
		}
		kind := "null"
		switch value.(type) {
		case string:
			kind = "string"
		case float64:
			kind = "number"
		case bool:
			kind = "boolean"
		case map[string]any:
			kind = "object"
		case []any:
			kind = "array"
		}
		if kind != want {
			return fmt.Errorf("field %s must be %s", key, want)
		}
	}
	return nil
}
