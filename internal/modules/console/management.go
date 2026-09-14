package console

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

// sandboxView intentionally excludes engine hosts, endpoints and staging identifiers.
type sandboxView struct {
	SessionID string
	Backend   string
	BackendID string
	Name      string
	State     string
	CPUs      uint32 `gorm:"column:cpus"`
	MemoryMiB uint64 `gorm:"column:memory_mib"`
	Image     string
	UpdatedAt time.Time
}

func sandboxRecord(r sandboxView) Record {
	return Record{ID: r.SessionID, Name: r.Name, State: r.State, SessionID: r.SessionID, CreatedAt: r.UpdatedAt, Meta: map[string]any{"backend": r.Backend, "backend_id": r.BackendID, "cpus": r.CPUs, "memory_mib": r.MemoryMiB, "image": r.Image, "updated_at": r.UpdatedAt}}
}
func browseSandboxes(ctx context.Context, db *gorm.DB, p *auth.Principal, in Query) (List, error) {
	out := List{Data: []Record{}}
	if in.TimeField != "" && in.TimeField != "created_at" {
		return out, apierr.Invalid("invalid time field")
	}
	sessions := auth.Owned(db.WithContext(ctx).Model(&execution.Session{}), p).Select("id")
	if in.EnvironmentID != "" {
		sessions = sessions.Where(clause.Eq{Column: "environment_id", Value: in.EnvironmentID})
	}
	q := db.WithContext(ctx).Table("sandbox_instances").Where(inSubquery{column: "session_id", query: sessions})
	if in.SessionID != "" {
		q = q.Where(clause.Eq{Column: "session_id", Value: in.SessionID})
	}
	if in.State != "" {
		q = q.Where(clause.Eq{Column: "state", Value: in.State})
	}
	if in.Search != "" {
		term := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(in.Search) + "%"
		q = q.Where(clause.Or(foldLike{"session_id", term}, foldLike{"name", term}, foldLike{"backend_id", term}))
	}
	for _, f := range []struct {
		value  string
		before bool
	}{{in.Before, true}, {in.After, false}} {
		if f.value == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, f.value)
		if err != nil {
			return out, apierr.Invalid("time filters require RFC3339")
		}
		if f.before {
			q = q.Where(clause.Lt{Column: "updated_at", Value: at})
		} else {
			q = q.Where(clause.Gte{Column: "updated_at", Value: at})
		}
	}
	if in.Cursor != "" {
		q = q.Where(clause.Lt{Column: "session_id", Value: in.Cursor})
	}
	var rows []sandboxView
	err := q.Select("session_id, backend, backend_id, name, state, cpus, memory_mib, image, updated_at").Order(clause.OrderByColumn{Column: clause.Column{Name: "session_id"}, Desc: true}).Limit(in.Limit + 1).Scan(&rows).Error
	if err != nil {
		return out, err
	}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.NextCursor = rows[len(rows)-1].SessionID
	}
	for _, r := range rows {
		out.Data = append(out.Data, sandboxRecord(r))
	}
	return out, nil
}
func skillRecord(r skills.Skill) Record {
	return Record{ID: r.ID, Name: r.Name, State: "available", CreatedAt: r.CreatedAt, Meta: map[string]any{"description": r.Description}}
}
func deploymentRecord(r deployments.Deployment) Record {
	state := "active"
	if r.Paused {
		state = "paused"
	}
	return Record{ID: r.ID, Name: r.Name, State: state, CreatedAt: r.CreatedAt, Meta: map[string]any{"agent_id": r.AgentID, "environment_id": r.EnvironmentID, "cron": r.Cron, "paused": r.Paused, "next_at": r.NextAt, "last_task_id": r.LastTask}}
}
