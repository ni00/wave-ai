package console

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/observe"
)

type rowCursor struct {
	At time.Time
	ID string
}

func browseLogs(ctx context.Context, db *gorm.DB, p *auth.Principal, in Query) (List, error) {
	out := List{Data: []Record{}}
	if in.Limit < 1 || in.Limit > 200 || len(in.Search) > 128 || len(in.Cursor) > 1024 || len(in.Module) > 128 {
		return out, apierr.Invalid("invalid log query")
	}
	q := auth.Owned(db.WithContext(ctx), p).Model(&observe.Log{})
	if in.State != "" {
		switch in.State {
		case "debug", "info", "warn", "error":
		default:
			return out, apierr.Invalid("invalid log level")
		}
		q = q.Where(clause.Eq{Column: "level", Value: in.State})
	}
	for k, v := range map[string]string{"task_id": in.TaskID, "session_id": in.SessionID, "trace_id": in.TraceID, "span_id": in.SpanID, "module": in.Module} {
		if v != "" {
			q = q.Where(clause.Eq{Column: k, Value: v})
		}
	}
	if in.Search != "" {
		term := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(in.Search) + "%"
		var expr []clause.Expression
		for _, col := range []string{"message", "module", "task_id", "trace_id", "span_id", "request_id", "error_text"} {
			expr = append(expr, foldLike{column: col, value: term})
		}
		q = q.Where(clause.Or(expr...))
	}
	for _, f := range []struct {
		raw    string
		before bool
	}{{in.Before, true}, {in.After, false}} {
		if f.raw == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, f.raw)
		if err != nil {
			return out, apierr.Invalid("invalid log time range")
		}
		if f.before {
			q = q.Where(clause.Lt{Column: "created_at", Value: at})
		} else {
			q = q.Where(clause.Gte{Column: "created_at", Value: at})
		}
	}
	if in.Cursor != "" {
		var cursor rowCursor
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.At.IsZero() || cursor.ID == "" {
			return out, apierr.Invalid("invalid log cursor")
		}
		q = q.Where(clause.Or(clause.Lt{Column: "created_at", Value: cursor.At}, clause.And(clause.Eq{Column: "created_at", Value: cursor.At}, clause.Lt{Column: "id", Value: cursor.ID})))
	}
	var rows []observe.Log
	if err := q.Omit("attributes", "error", "error_text").Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).Limit(in.Limit + 1).Find(&rows).Error; err != nil {
		return out, err
	}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		last := rows[len(rows)-1]
		raw, _ := json.Marshal(rowCursor{last.CreatedAt, last.ID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, r := range rows {
		out.Data = append(out.Data, Record{ID: r.ID, Name: r.Message, State: r.Level, CreatedAt: r.CreatedAt, SessionID: r.SessionID, TaskID: r.TaskID, RootID: r.TraceID, Meta: map[string]any{"module": r.Module, "span_id": r.SpanID, "request_id": r.RequestID}})
	}
	return out, nil
}
