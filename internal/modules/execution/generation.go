package execution

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

// Generation measures one provider call, including retries and compaction.
// Null usage or finish time means unknown, never zero cost or successful completion.
type Generation struct {
	logContext   context.Context
	ID           string            `gorm:"primaryKey" json:"id" validate:"required"`
	TaskID       string            `gorm:"index" json:"task_id" validate:"required"`
	MessageID    string            `json:"message_id,omitempty"`
	Purpose      string            `json:"purpose" validate:"required"`
	Model        string            `json:"model" validate:"required"`
	Attempt      int               `json:"attempt" validate:"required"`
	State        string            `json:"state" validate:"required" enums:"running,completed,failed,interrupted"`
	StartedAt    time.Time         `json:"started_at" validate:"required" format:"date-time"`
	FinishedAt   *time.Time        `json:"finished_at,omitempty" format:"date-time"`
	DurationMS   *int64            `json:"duration_ms" validate:"required" format:"int64" extensions:"x-nullable"`    // 耗时，单位毫秒；未知时为 null。 || Duration in milliseconds; null when unknown.
	FirstDeltaMS *int64            `json:"first_delta_ms" validate:"required" format:"int64" extensions:"x-nullable"` // 首个增量的延迟，单位毫秒；尚未收到或未知时为 null。 || First-delta latency in milliseconds; null if no delta has arrived or the value is unknown.
	Usage        modelclient.Usage `gorm:"serializer:json;type:jsonb" json:"usage" validate:"required"`
	Error        string            `json:"error,omitempty"`
}

func (w *Worker) startGeneration(ctx context.Context, claim *Task, t Task, purpose, messageID string) (Generation, error) {
	g := Generation{ID: xid.New("generation"), TaskID: t.ID, MessageID: messageID, Purpose: purpose, Model: t.Snapshot.Model, Attempt: t.Attempts, State: "running", StartedAt: time.Now()}
	e := fenced(ctx, w.DB, claim, func(tx *gorm.DB, s *Session, current *Task) error {
		if e := tx.Create(&g).Error; e != nil {
			return e
		}
		return emit(tx, s, t.ID, "generation.started", map[string]any{"generation_id": g.ID, "message_id": messageID, "purpose": purpose, "attempt": g.Attempt})
	})
	scope := observe.From(ctx)
	scope.SpanID = g.ID
	g.logContext = observe.With(ctx, scope)
	if e == nil {
		observe.Logger(g.logContext).Info("model.started", "model", g.Model, "attempt", g.Attempt, "purpose", g.Purpose)
	}
	return g, e
}
func (g *Generation) complete(final *modelclient.Final, err error) {
	now := time.Now()
	elapsed := now.Sub(g.StartedAt).Milliseconds()
	g.FinishedAt = &now
	g.DurationMS = &elapsed
	g.State = "completed"
	if final != nil {
		g.Usage = final.Usage
	}
	defer func() {
		ctx := g.logContext
		if ctx == nil {
			ctx = context.Background()
		}
		observe.Logger(ctx).Info("model.finished", "model", g.Model, "state", g.State, "duration_ms", elapsed, "first_delta_ms", g.FirstDeltaMS, "usage", g.Usage)
	}()
	if err != nil {
		g.State = "failed"
		g.Error = err.Error()
	}
}

func interruptGenerations(tx *gorm.DB, t *Task) error {
	res := tx.Model(&Generation{}).Where(clause.And(eq("task_id", t.ID), eq("state", "running"))).Update("state", "interrupted")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		t.UsageKnown = false
		if e := tx.Model(&Task{}).Where(eq("id", t.ID)).Update("usage_known", false).Error; e != nil {
			return e
		}
		if t.ParentID != "" {
			return tx.Model(&Task{}).Where(eq("id", t.RootID)).Update("usage_known", false).Error
		}
	}
	return nil
}
