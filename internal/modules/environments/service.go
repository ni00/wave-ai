package environments

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/auth"
)

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Environment, error) {
	var out Environment
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&out).Error
	return out, e
}
