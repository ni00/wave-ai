// Package console supplies bounded, owner-scoped read models for the web console.
package console

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

const BenchMIME = "application/vnd.wave.bench+json"

type Record struct {
	ID        string         `json:"id" validate:"required"`
	Name      string         `json:"name" validate:"required"`
	State     string         `json:"state" validate:"required"`
	CreatedAt time.Time      `json:"created_at" validate:"required" format:"date-time"`
	SessionID string         `json:"session_id,omitempty"`
	TaskID    string         `json:"task_id,omitempty"`
	RootID    string         `json:"root_id,omitempty"`
	Meta      map[string]any `json:"meta" validate:"required"`
}
type List struct {
	Data       []Record `json:"data" validate:"required"`
	NextCursor string   `json:"next_cursor,omitempty"`
}
type Query struct {
	Kind, Search, State, SessionID, TaskID, Before, After, Cursor string
	TraceID, SpanID, Module, TimeField                            string
	AgentID, EnvironmentID, SkillID                               string
	Limit                                                         int
}

// Projection omits snapshots, prompts, tool arguments and event payloads from lists.
func Browse(ctx context.Context, db *gorm.DB, p *auth.Principal, in Query) (List, error) {
	out := List{Data: []Record{}}
	if in.Kind == "logs" {
		return browseLogs(ctx, db, p, in)
	}
	if in.Limit < 1 || in.Limit > 200 || len(in.Search) > 128 || len(in.Cursor) > 1024 {
		return out, apierr.Invalid("invalid page size, search or cursor")
	}
	if in.Kind == "sandboxes" {
		return browseSandboxes(ctx, db, p, in)
	}
	q := db.WithContext(ctx)
	owned := func(q *gorm.DB) *gorm.DB { return auth.Owned(q, p) }
	sessions := owned(db.WithContext(ctx).Model(&execution.Session{})).Select("id")
	var model any
	var searchColumns []string
	switch in.Kind {
	case "traces", "tasks":
		model = &execution.Task{}
		q = q.Where(inSubquery{column: "session_id", query: sessions})
		searchColumns = []string{"id", "agent_id"}
	case "agents":
		model = &agents.Agent{}
		q = owned(q)
		searchColumns = []string{"id"}
		if in.SkillID != "" {
			b, _ := json.Marshal([]string{in.SkillID})
			q = q.Where(clause.Expr{SQL: "config->'skill_ids' @> ?::jsonb", Vars: []any{string(b)}})
		}
	case "skills":
		model = &skills.Skill{}
		q = owned(q)
		searchColumns = []string{"id", "name", "description"}
	case "deployments":
		model = &deployments.Deployment{}
		q = owned(q)
		searchColumns = []string{"id", "name"}
	case "sessions":
		model = &execution.Session{}
		q = owned(q)
		searchColumns = []string{"id", "title"}
	case "environments":
		model = &environments.Environment{}
		q = owned(q)
		searchColumns = []string{"id", "name"}
	case "files", "benchmarks":
		model = &files.File{}
		q = owned(q).Where(clause.Eq{Column: "deleted", Value: false})
		searchColumns = []string{"id", "name"}
		if in.Kind == "benchmarks" {
			q = q.Where(clause.Eq{Column: "mime", Value: BenchMIME})
		}
	case "memory":
		model = &memory.Store{}
		q = owned(q)
		searchColumns = []string{"id", "name"}
	case "events":
		model = &execution.Event{}
		q = q.Where(inSubquery{column: "session_id", query: sessions}).Where(clause.Neq{Column: "type", Value: "message.delta"})
		searchColumns = []string{"task_id", "type"}
	default:
		return out, apierr.Invalid("unknown resource kind")
	}
	q = q.Model(model)
	if in.Search != "" {
		term := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(in.Search)
		var expr []clause.Expression
		for _, name := range searchColumns {
			expr = append(expr, foldLike{column: name, value: "%" + term + "%"})
		}
		if in.Kind == "agents" {
			expr = append(expr, clause.Expr{SQL: "config->>'name' ILIKE ?", Vars: []any{"%" + term + "%"}})
		}
		if in.Kind == "tasks" || in.Kind == "traces" {
			titleSessions := owned(db.WithContext(ctx).Model(&execution.Session{})).Select("id").Where(foldLike{column: "title", value: "%" + term + "%"})
			expr = append(expr, inSubquery{column: "session_id", query: titleSessions})
		}
		q = q.Where(clause.Or(expr...))
	}
	if in.SessionID != "" && (in.Kind == "tasks" || in.Kind == "traces" || in.Kind == "events" || in.Kind == "files") {
		q = q.Where(clause.Eq{Column: "session_id", Value: in.SessionID})
	}
	if in.TaskID != "" && (in.Kind == "events" || in.Kind == "files") {
		q = q.Where(clause.Eq{Column: "task_id", Value: in.TaskID})
	}
	if in.AgentID != "" && (in.Kind == "tasks" || in.Kind == "traces" || in.Kind == "deployments") {
		q = q.Where(clause.Eq{Column: "agent_id", Value: in.AgentID})
	}
	if in.EnvironmentID != "" && (in.Kind == "sessions" || in.Kind == "deployments") {
		q = q.Where(clause.Eq{Column: "environment_id", Value: in.EnvironmentID})
	}
	if in.State != "" {
		switch in.Kind {
		case "tasks", "traces":
			q = q.Where(clause.Eq{Column: "state", Value: in.State})
		case "environments", "sessions", "agents":
			if in.State != "active" && in.State != "archived" {
				return out, apierr.Invalid("state must be active or archived")
			}
			q = q.Where(clause.Eq{Column: "archived", Value: in.State == "archived"})
		case "deployments":
			if in.State != "active" && in.State != "paused" {
				return out, apierr.Invalid("state must be active or paused")
			}
			q = q.Where(clause.Eq{Column: "paused", Value: in.State == "paused"})
		case "events":
			q = q.Where(clause.Eq{Column: "type", Value: in.State})
		default:
			return out, apierr.Invalid("state filter unsupported for this resource")
		}
	}
	timeColumn := "created_at"
	if in.TimeField != "" && in.TimeField != "created_at" {
		if (in.Kind != "tasks" && in.Kind != "traces") || in.TimeField != "finished_at" {
			return out, apierr.Invalid("invalid time field")
		}
		timeColumn = "finished_at"
	}
	for _, f := range []struct {
		raw    string
		before bool
	}{{in.Before, true}, {in.After, false}} {
		if f.raw == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339Nano, f.raw)
		if err != nil {
			return out, apierr.Invalid("time filters require RFC3339")
		}
		if f.before {
			q = q.Where(clause.Lt{Column: timeColumn, Value: value})
		} else {
			q = q.Where(clause.Gte{Column: timeColumn, Value: value})
		}
	}
	if in.Kind == "events" {
		if in.Cursor != "" {
			var c logCursor
			b, err := base64.RawURLEncoding.DecodeString(in.Cursor)
			if err != nil || json.Unmarshal(b, &c) != nil || c.At.IsZero() || c.Session == "" || c.Seq < 1 {
				return out, apierr.Invalid("invalid log cursor")
			}
			q = q.Where(clause.Or(clause.Lt{Column: "created_at", Value: c.At}, clause.And(clause.Eq{Column: "created_at", Value: c.At}, clause.Lt{Column: "session_id", Value: c.Session}), clause.And(clause.Eq{Column: "created_at", Value: c.At}, clause.Eq{Column: "session_id", Value: c.Session}, clause.Lt{Column: "seq", Value: c.Seq})))
		}
		q = q.Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).Order(clause.OrderByColumn{Column: clause.Column{Name: "session_id"}, Desc: true}).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}, Desc: true})
		var rows []execution.Event
		if err := q.Omit("data").Limit(in.Limit + 1).Find(&rows).Error; err != nil {
			return out, err
		}
		if len(rows) > in.Limit {
			rows = rows[:in.Limit]
			last := rows[len(rows)-1]
			b, _ := json.Marshal(logCursor{last.CreatedAt, last.SessionID, last.Seq})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
		}
		for _, r := range rows {
			out.Data = append(out.Data, Record{ID: r.SessionID + ":" + strconv.FormatInt(r.Seq, 10), Name: r.Type, State: logLevel(r.Type), CreatedAt: r.CreatedAt, SessionID: r.SessionID, TaskID: r.TaskID, Meta: map[string]any{"sequence": r.Seq, "source": "execution_event"}})
		}
		return out, nil
	}
	if in.Cursor != "" {
		q = q.Where(clause.Lt{Column: "id", Value: in.Cursor})
	}
	q = q.Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).Limit(in.Limit + 1)
	switch in.Kind {
	case "tasks", "traces":
		var rows []execution.Task
		if err := q.Select("id", "agent_id", "agent_version", "state", "session_id", "root_id", "created_at", "started_at", "finished_at", "used_tokens", "tool_count", "attempts", "usage_known").Find(&rows).Error; err != nil {
			return out, err
		}
		ids := make([]any, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.SessionID)
		}
		var sessionRows []execution.Session
		if len(ids) > 0 {
			if err := owned(db.WithContext(ctx)).Select("id", "title").Where(clause.IN{Column: "id", Values: ids}).Find(&sessionRows).Error; err != nil {
				return out, err
			}
		}
		titles := map[string]string{}
		for _, r := range sessionRows {
			titles[r.ID] = r.Title
		}

		for _, r := range rows {
			var ms *float64
			if r.FinishedAt != nil {
				v := float64(r.FinishedAt.Sub(r.CreatedAt)) / float64(time.Millisecond)
				ms = &v
			}
			out.Data = append(out.Data, Record{ID: r.ID, Name: taskName(titles[r.SessionID], r.AgentID), State: r.State, CreatedAt: r.CreatedAt, SessionID: r.SessionID, TaskID: r.ID, RootID: r.RootID, Meta: map[string]any{"duration_ms": ms, "tokens": r.UsedTokens, "usage_known": r.UsageKnown, "tool_calls": r.ToolCount, "model_calls": r.Attempts, "agent_version": r.AgentVersion}})
		}
	case "agents":
		var rows []struct {
			ID        string
			Name      string
			Model     string
			Version   int
			Archived  bool
			CreatedAt time.Time
		}
		if err := q.Select("id, config->>'name' AS name, config->>'model' AS model, version, archived, created_at").Scan(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			state := "active"
			if r.Archived {
				state = "archived"
			}
			out.Data = append(out.Data, Record{ID: r.ID, Name: r.Name, State: state, CreatedAt: r.CreatedAt, Meta: map[string]any{"model": r.Model, "version": r.Version}})
		}
	case "skills":
		var rows []skills.Skill
		if err := q.Omit("blob_key").Find(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			out.Data = append(out.Data, skillRecord(r))
		}
	case "deployments":
		var rows []deployments.Deployment
		if err := q.Omit("input").Find(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			out.Data = append(out.Data, deploymentRecord(r))
		}

	case "sessions":
		var rows []execution.Session
		if err := q.Select("id", "title", "environment_id", "active_root", "archived", "created_at").Find(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			state := "active"
			if r.Archived {
				state = "archived"
			}
			out.Data = append(out.Data, Record{ID: r.ID, Name: r.Title, State: state, CreatedAt: r.CreatedAt, SessionID: r.ID, Meta: map[string]any{"environment_id": r.EnvironmentID, "active_task_id": r.ActiveRoot}})
		}
	case "environments":
		var rows []environments.Environment
		if err := q.Omit("packages").Find(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			state := "active"
			if r.Archived {
				state = "archived"
			}
			out.Data = append(out.Data, Record{ID: r.ID, Name: r.Name, State: state, CreatedAt: r.CreatedAt, Meta: map[string]any{"backend": r.SandboxBackend, "profile": r.SandboxProfile}})
		}
	case "files", "benchmarks":
		var rows []files.File
		if err := q.Select("id", "name", "mime", "size", "path", "created_at", "task_id", "session_id").Find(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			out.Data = append(out.Data, Record{ID: r.ID, Name: r.Name, State: "available", CreatedAt: r.CreatedAt, TaskID: r.TaskID, SessionID: r.SessionID, Meta: map[string]any{"mime": r.MIME, "size": r.Size, "path": r.Path}})
		}
	case "memory":
		var rows []memory.Store
		if err := q.Select("id", "name", "created_at").Find(&rows).Error; err != nil {
			return out, err
		}
		for _, r := range rows {
			out.Data = append(out.Data, Record{ID: r.ID, Name: r.Name, State: "active", CreatedAt: r.CreatedAt, Meta: map[string]any{}})
		}
	}
	if len(out.Data) > in.Limit {
		out.Data = out.Data[:in.Limit]
		out.NextCursor = out.Data[len(out.Data)-1].ID
	}
	return out, nil
}

type logCursor struct {
	At      time.Time
	Session string
	Seq     int64
}

func logLevel(kind string) string {
	if strings.Contains(kind, "failed") || strings.Contains(kind, "unknown") {
		return "error"
	}
	if strings.Contains(kind, "interrupted") {
		return "warn"
	}
	return "info"
}

func taskName(title, agent string) string {
	if title != "" {
		return title
	}
	return agent
}

// GORM's clause.IN treats a one-element value slice as equality and does not
// parenthesize *gorm.DB. Keep the column quoted and the subquery parameterized.
type inSubquery struct {
	column string
	query  *gorm.DB
}

func (in inSubquery) Build(b clause.Builder) {
	b.WriteQuoted(clause.Column{Name: in.column})
	b.WriteString(" IN (")
	b.AddVar(b, in.query)
	b.WriteByte(')')
}

type foldLike struct{ column, value string }

func (in foldLike) Build(b clause.Builder) {
	b.WriteQuoted(clause.Column{Name: in.column})
	b.WriteString(" ILIKE ")
	b.AddVar(b, in.value)
}
