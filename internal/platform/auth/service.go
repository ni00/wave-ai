package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
)

func Authenticate(ctx context.Context, db *gorm.DB, bearer string) (*Principal, error) {
	var key APIKey
	err := db.WithContext(ctx).Where(clause.Eq{Column: "key_hash", Value: hashKey(bearer)}).Take(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apierr.Unauthorized("invalid api key")
	}
	if err != nil {
		return nil, apierr.InternalErr(err, "authenticate failed")
	}
	if key.RevokedAt != nil {
		return nil, apierr.Unauthorized("api key revoked")
	}
	return &Principal{OrgID: key.OrgID, PrincipalID: key.PrincipalID, APIKeyID: key.ID, Scope: key.Scope}, nil
}

// Bootstrap creates the identity and key atomically; plaintext is returned only
// after commit. Repeating an organization/user reuses their stable identities.
func Bootstrap(ctx context.Context, db *gorm.DB, orgName, userName, label, scope string) (string, error) {
	orgName, userName = strings.TrimSpace(orgName), strings.TrimSpace(userName)
	if orgName == "" || userName == "" {
		return "", apierr.Invalid("organization and user names are required")
	}
	if scope != ScopeAPI {
		return "", apierr.Invalid("scope must be api")
	}
	raw, hash, prefix := GenerateKey()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		org := Organization{ID: newID(), Name: orgName}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).Create(&org).Error; err != nil {
			return err
		}
		// A concurrent insert may have supplied a different ID.
		org = Organization{}
		if err := tx.Where(clause.Eq{Column: "name", Value: orgName}).Take(&org).Error; err != nil {
			return err
		}
		identity := Identity{ID: newID(), OrgID: org.ID, DisplayName: userName}
		if err := tx.Omit(clause.Associations).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "org_id"}, {Name: "display_name"}}, DoNothing: true}).Create(&identity).Error; err != nil {
			return err
		}
		identity = Identity{}
		if err := tx.Where(clause.And(clause.Eq{Column: "org_id", Value: org.ID}, clause.Eq{Column: "display_name", Value: userName})).Take(&identity).Error; err != nil {
			return err
		}
		key := APIKey{ID: newID(), OrgID: org.ID, PrincipalID: identity.ID, KeyHash: hash, KeyPrefix: prefix, Label: label, Scope: scope}
		return tx.Omit(clause.Associations).Create(&key).Error
	})
	if err != nil {
		return "", fmt.Errorf("bootstrap: %w", err)
	}
	return raw, nil
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func GenerateKey() (key, hash, prefix string) {
	var random [32]byte
	_, _ = rand.Read(random[:])
	key = "wa-" + base64.RawURLEncoding.EncodeToString(random[:])
	return key, hashKey(key), key[:10]
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
