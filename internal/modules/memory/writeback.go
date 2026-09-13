package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/auth"
)

// WriteBack compares the sandbox projection to its saved starting baseline.
func WriteBack(ctx context.Context, db *gorm.DB, p *auth.Principal, sid, id string, local map[string][]byte) error {
	if _, e := Get(ctx, db, p, id); e != nil {
		return e
	}
	var e error
	base := []Baseline{}
	if e = db.WithContext(ctx).Where(clause.And(clause.Eq{Column: "session_id", Value: sid}, clause.Eq{Column: "store_id", Value: id})).Find(&base).Error; e != nil {
		return e
	}
	baselines := map[string]Baseline{}
	for _, b := range base {
		baselines[b.Path] = b
		if _, ok := local[b.Path]; !ok {
			local[b.Path] = nil
		}
	}
	for name, data := range local {
		b := baselines[name]
		sum := sha256.Sum256(data)
		sha := fmt.Sprintf("%x", sum[:])
		deleted := data == nil
		if !deleted && sha == b.SHA {
			continue
		}
		var current Entry
		if e = db.WithContext(ctx).Where(clause.And(clause.Eq{Column: "store_id", Value: id}, clause.Eq{Column: "path", Value: name})).Limit(1).Find(&current).Error; e != nil {
			return e
		}
		if current.Version > 0 && ((deleted && current.Deleted) || (!deleted && !current.Deleted && current.SHA == sha)) {
			b.Version = current.Version
			b.SHA = current.SHA
		} else {
			var updated Entry
			if deleted {
				updated, e = Delete(ctx, db, p, id, name, sid, b.Version)
			} else {
				updated, e = Write(ctx, db, p, id, name, string(data), sid, b.Version)
			}
			if e != nil {
				return e
			}
			b.Version = updated.Version
			b.SHA = updated.SHA
		}
		b.SessionID = sid
		b.StoreID = id
		b.Path = name
		if deleted {
			if e = db.WithContext(ctx).Where(clause.And(clause.Eq{Column: "session_id", Value: sid}, clause.Eq{Column: "store_id", Value: id}, clause.Eq{Column: "path", Value: name})).Delete(&Baseline{}).Error; e != nil {
				return e
			}
		} else if e = db.WithContext(ctx).Save(&b).Error; e != nil {
			return e
		}
	}
	return nil
}

// Project saves one consistent store snapshot before filesystem staging.
func Project(ctx context.Context, db *gorm.DB, p *auth.Principal, sid, id string) ([]Entry, error) {
	rows := []Entry{}
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := Get(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), p, id); e != nil {
			return e
		}
		if e := tx.Where(clause.And(clause.Eq{Column: "session_id", Value: sid}, clause.Eq{Column: "store_id", Value: id})).Delete(&Baseline{}).Error; e != nil {
			return e
		}
		if e := tx.Where(clause.Eq{Column: "store_id", Value: id}).Find(&rows).Error; e != nil {
			return e
		}
		for _, row := range rows {
			if row.Deleted {
				row.SHA = ""
			}
			if e := tx.Create(&Baseline{SessionID: sid, StoreID: id, Path: row.Path, Version: row.Version, SHA: row.SHA}).Error; e != nil {
				return e
			}
		}
		return nil
	})
	return rows, e
}
