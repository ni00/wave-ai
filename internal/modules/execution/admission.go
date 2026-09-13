package execution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

type Receipt struct {
	PrincipalID string `gorm:"primaryKey"`
	Operation   string `gorm:"primaryKey"`
	Key         string `gorm:"primaryKey"`
	Digest      string
	Response    []byte
	CreatedAt   time.Time
}

// Admit deduplicates client retries atomically with the underlying use case.
// A key is private to an identity and operation; reuse with another body fails.
func Admit[T any](ctx context.Context, db *gorm.DB, p *auth.Principal, key, operation string, body any, run func(*gorm.DB) (T, error)) (T, error) {
	var out T
	if key == "" {
		return run(db)
	}
	if len(key) > 128 {
		return out, apierr.Invalid("Idempotency-Key exceeds 128 bytes")
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return out, e
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		receipt := Receipt{PrincipalID: p.PrincipalID, Operation: operation, Key: key, Digest: digest}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error; e != nil {
			return e
		}
		if e := tx.Clauses(lock).Where(clause.And(eq("principal_id", p.PrincipalID), eq("operation", operation), eq("key", key))).Take(&receipt).Error; e != nil {
			return e
		}
		if receipt.Digest != digest {
			return apierr.New(409, apierr.InvalidRequest, "Idempotency-Key already used with another request")
		}
		if len(receipt.Response) > 0 {
			return json.Unmarshal(receipt.Response, &out)
		}
		var e error
		out, e = run(tx)
		if e != nil {
			return e
		}
		receipt.Response, e = json.Marshal(out)
		if e != nil {
			return e
		}
		return tx.Save(&receipt).Error
	})
	return out, e
}
