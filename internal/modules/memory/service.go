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

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Store, error) {
	var s Store
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&s).Error
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
			return tx.Create(&Conflict{ID: xid.New("conflict"), StoreID: id, Path: key, Expected: expected, Actual: row.Version, Content: content}).Error
		}
		sum := sha256.Sum256([]byte(content))
		row = Entry{StoreID: id, Path: key, Version: expected + 1, Content: content, SHA: hex.EncodeToString(sum[:]), Deleted: deleted}
		if e = tx.Save(&row).Error; e != nil {
			return e
		}
		return tx.Create(&Revision{ID: xid.New("revision"), StoreID: id, Path: key, Version: row.Version, Content: content, SessionID: sid, Deleted: deleted}).Error
	})
	if err == nil && conflict {
		err = apierr.New(409, apierr.InvalidRequest, "memory version conflict; conflicting content retained")
	}
	return row, err
}
