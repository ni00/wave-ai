package execution

import (
	"context"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

func Steer(ctx context.Context, db *gorm.DB, p *auth.Principal, id, text string) error {
	return steer(ctx, db, p, id, text, "")
}
func steer(ctx context.Context, db *gorm.DB, p *auth.Principal, id, text, sourceTaskID string) error {
	if text == "" {
		return apierr.Invalid("text is required")
	}
	return withTask(ctx, db, id, func(tx *gorm.DB, s *Session, t *Task) error {
		if e := authorize(s, p); e != nil {
			return e
		}
		if Terminal(t.State) || t.CancelRequested || t.Finalizing {
			return apierr.New(409, apierr.InvalidRequest, "task no longer accepts input")
		}
		if e := addInput(tx, s, t, text, inputSource(sourceTaskID), sourceTaskID); e != nil {
			return e
		}
		if t.State == "waiting" && t.PendingFinish != "" {
			t.PendingFinish = ""
			t.State = "queued"
			if e := tx.Save(t).Error; e != nil {
				return e
			}
		}
		return emit(tx, s, id, "input.accepted", map[string]any{"text": text})
	})
}
