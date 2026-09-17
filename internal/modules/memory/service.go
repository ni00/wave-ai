package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

// Quotas keep unbounded growth out of the database: every write persists a
// full-content revision row, so history needs a retention bound. Tests may
// lower these values.
var (
	contentLimitBytes = 1 << 20
	storeLimit        = 20
	entryLimit        = 1000
	historyKeep       = 10
)

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Store, error) {
	var s Store
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&s).Error
	return s, e
}
func Create(ctx context.Context, db *gorm.DB, p *auth.Principal, name string) (Store, error) {
	s := Store{ID: xid.New("memory"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: name}
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize admission per identity to keep per-owner store counts bounded.
		var identity auth.Identity
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(clause.Eq{Column: "id", Value: p.PrincipalID}).Take(&identity).Error; e != nil {
			return e
		}
		var count int64
		if e := auth.Owned(tx.Model(&Store{}), p).Count(&count).Error; e != nil {
			return e
		}
		if count >= int64(storeLimit) {
			return apierr.Invalid("at most %d memory stores per owner", storeLimit)
		}
		return tx.Create(&s).Error
	})
	return s, e
}
func Write(ctx context.Context, db *gorm.DB, p *auth.Principal, id, key, content, sid string, expected int) (Entry, error) {
	return change(ctx, db, p, id, key, content, sid, expected, false)
}
func Delete(ctx context.Context, db *gorm.DB, p *auth.Principal, id, key, sid string, expected int) (Entry, error) {
	return change(ctx, db, p, id, key, "", sid, expected, true)
}
func change(ctx context.Context, db *gorm.DB, p *auth.Principal, id, key, content, sid string, expected int, deleted bool) (Entry, error) {
	var row Entry
	if key == "" || path.Clean(key) == "." || path.IsAbs(key) || strings.HasPrefix(path.Clean(key), "..") || strings.Contains(key, "\\") {
		return row, apierr.Invalid("memory path must be relative")
	}
	if len(content) > contentLimitBytes {
		return row, apierr.Invalid("memory entry exceeds 1 MiB")
	}
	key = path.Clean(key)
	conflict := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := Get(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), p, id); e != nil {
			return e
		}
		e := tx.Where(clause.And(clause.Eq{Column: "store_id", Value: id}, clause.Eq{Column: "path", Value: key})).Limit(1).Find(&row).Error
		if e != nil {
			return e
		}
		if row.Version != expected {
			conflict = true
			if e := tx.Create(&Conflict{ID: xid.New("conflict"), StoreID: id, Path: key, Expected: expected, Actual: row.Version, Content: content}).Error; e != nil {
				return e
			}
			keep := []string{}
			if e := tx.Model(&Conflict{}).Where(clause.And(clause.Eq{Column: "store_id", Value: id}, clause.Eq{Column: "path", Value: key})).
				Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).
				Limit(historyKeep).Pluck("id", &keep).Error; e != nil {
				return e
			}
			stale := make([]any, len(keep))
			for i, k := range keep {
				stale[i] = k
			}
			// The store row is locked FOR UPDATE in this transaction, so the
			// kept set cannot change between the Pluck and the Delete.
			return tx.Where(clause.And(clause.Eq{Column: "store_id", Value: id}, clause.Eq{Column: "path", Value: key},
				clause.Not(clause.IN{Column: "id", Values: stale}))).Delete(&Conflict{}).Error
		}
		if row.Version == 0 {
			var count int64
			if e := tx.Model(&Entry{}).Where(clause.Eq{Column: "store_id", Value: id}).Count(&count).Error; e != nil {
				return e
			}
			if count >= int64(entryLimit) {
				return apierr.Invalid("memory store is limited to %d entries", entryLimit)
			}
		}
		sum := sha256.Sum256([]byte(content))
		row = Entry{StoreID: id, Path: key, Version: expected + 1, Content: content, SHA: hex.EncodeToString(sum[:]), Deleted: deleted}
		if e = tx.Save(&row).Error; e != nil {
			return e
		}
		if e := tx.Create(&Revision{ID: xid.New("revision"), StoreID: id, Path: key, Version: row.Version, Content: content, SessionID: sid, Deleted: deleted}).Error; e != nil {
			return e
		}
		return tx.Where(clause.And(clause.Eq{Column: "store_id", Value: id}, clause.Eq{Column: "path", Value: key},
			clause.Lte{Column: "version", Value: row.Version - historyKeep})).Delete(&Revision{}).Error
	})
	if err == nil && conflict {
		err = apierr.New(409, apierr.InvalidRequest, "memory version conflict; conflicting content retained")
	}
	return row, err
}
