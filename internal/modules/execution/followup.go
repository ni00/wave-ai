package execution

import (
	"context"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

// Followup resumes a finished child while its coordinator still owns the session.
// Context and cumulative budgets remain attached to the same child.
func Followup(ctx context.Context, db *gorm.DB, p *auth.Principal, id, text string) error {
	return followup(ctx, db, p, id, text, "")
}
func followup(ctx context.Context, db *gorm.DB, p *auth.Principal, id, text, sourceTaskID string) error {
	if text == "" {
		return apierr.Invalid("text required")
	}
	return withTask(ctx, db, id, func(tx *gorm.DB, s *Session, t *Task) error {
		if e := authorize(s, p); e != nil {
			return e
		}
		if t.ParentID == "" || !Terminal(t.State) {
			return apierr.Invalid("only finished children can be resumed")
		}
		var parent Task
		if e := tx.Where(eq("id", t.ParentID)).Take(&parent).Error; e != nil {
			return e
		}
		if Terminal(parent.State) || parent.Finalizing || parent.CancelRequested {
			return apierr.Invalid("coordinator no longer accepts collaboration")
		}
		t.Messages = completeHistory(t.Messages)
		t.State = "queued"
		t.FinishedAt = nil
		t.Evaluation = nil
		t.PendingFinish = ""
		t.Finalizing = false
		t.CancelRequested = false
		t.Error = ""
		t.Failures = 0
		if e := addInput(tx, s, t, text, inputSource(sourceTaskID), sourceTaskID); e != nil {
			return e
		}
		if e := tx.Save(t).Error; e != nil {
			return e
		}
		return emit(tx, s, id, "agent.resumed", map[string]any{"text": text})
	})
}
