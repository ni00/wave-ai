package execution

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

func Archive(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		s, e := session(ctx, tx.Clauses(lock), p, id)
		if e != nil {
			return e
		}
		var count int64
		if e = tx.Model(&Task{}).Where(clause.And(eq("session_id", id), eq("finished_at", nil))).Count(&count).Error; e != nil {
			return e
		}
		if count > 0 || s.ActiveRoot != "" {
			return apierr.New(409, apierr.InvalidRequest, "session has unfinished tasks")
		}
		return tx.Model(&s).Update("archived", true).Error
	})
}
