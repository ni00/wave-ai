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
	if c.VaultID != "" {
		v, err := Get(ctx, db, p, c.VaultID)
		if err != nil {
			return "", err
		}
		if v.Archived {
			return "", apierr.Forbidden("vault archived")
		}
	}
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "https" || u.Host != c.Host || u.User != nil {
		return "", apierr.Forbidden("credential target mismatch")
	}
	raw, e := box.Open(c.Ciphertext, c.Nonce)
	return string(raw), e
}

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Vault, error) {
	var v Vault
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&v).Error
	return v, e
}
func Authorize(ctx context.Context, db *gorm.DB, p *auth.Principal, c Credential, vaultIDs []string) error {
	if c.VaultID == "" {
		return nil
	}
	bound := false
	for _, id := range vaultIDs {
		if id == c.VaultID {
			bound = true
			break
		}
	}
	if !bound {
		return apierr.Forbidden("credential vault is not bound to this session")
	}
	v, e := Get(ctx, db, p, c.VaultID)
	if e != nil {
		return e
	}
	if v.Archived {
		return apierr.Forbidden("vault archived")
	}
	return nil
}
func SessionBearer(ctx context.Context, db *gorm.DB, box *secrets.Box, p *auth.Principal, id, target string, vaultIDs []string) (string, error) {
	var c Credential
	if e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&c).Error; e != nil {
		return "", e
	}
	if e := Authorize(ctx, db, p, c, vaultIDs); e != nil {
		return "", e
	}
	return Bearer(ctx, db, box, p, id, target)
}
func Rotate(ctx context.Context, db *gorm.DB, box *secrets.Box, p *auth.Principal, id string, in RotateRequest) (Credential, error) {
	var c Credential
	if in.Token == "" || len(in.Token) > 65536 {
		return c, apierr.Invalid("token must contain 1 to 65536 bytes")
	}
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := auth.Owned(tx.Clauses(clause.Locking{Strength: "UPDATE"}), p).Where(clause.Eq{Column: "id", Value: id}).Take(&c).Error; e != nil {
			return e
		}
		if c.Revoked {
			return apierr.Conflict("credential revoked")
		}
		if c.Version != in.Version {
			return apierr.Conflict("credential version conflict")
		}
		cipher, nonce, e := box.Seal([]byte(in.Token))
		if e != nil {
			return e
		}
		c.Ciphertext = cipher
		c.Nonce = nonce
		c.Version++
		return tx.Save(&c).Error
	})
	return c, e
}
