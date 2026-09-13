package vault

import (
	"context"
	"net/url"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/secrets"
)

func Bearer(ctx context.Context, db *gorm.DB, box *secrets.Box, p *auth.Principal, id, target string) (string, error) {
	var c Credential
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.And(clause.Eq{Column: "id", Value: id}, clause.Eq{Column: "revoked", Value: false})).Take(&c).Error
	if e != nil {
		return "", e
	}
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "https" || u.Host != c.Host {
		return "", apierr.Forbidden("credential target mismatch")
	}
	raw, e := box.Open(c.Ciphertext, c.Nonce)
	return string(raw), e
}
