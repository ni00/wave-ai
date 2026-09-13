package files

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"path"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/xid"
)

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (File, error) {
	var f File
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.And(clause.Eq{Column: "id", Value: id}, clause.Eq{Column: "deleted", Value: false})).Take(&f).Error
	return f, e
}
func Put(ctx context.Context, db *gorm.DB, b *blobstore.Store, p *auth.Principal, sid, name string, data []byte) (File, error) {
	f := File{ID: xid.New("file"), OrgID: p.OrgID, OwnerID: p.PrincipalID, SessionID: sid, Name: name, MIME: http.DetectContentType(data)}
	if name == "" || path.Base(name) != name || strings.ContainsAny(name, "\\\x00") {
		return f, apierr.Invalid("invalid filename")
	}
	key, size, e := b.PutBytes(ctx, blobstore.Scope{OrgID: p.OrgID, OwnerID: p.PrincipalID}, data)
	if e != nil {
		return f, e
	}
	f.BlobKey = key
	f.Size = size
	return f, db.WithContext(ctx).Create(&f).Error
}

func PutArtifact(ctx context.Context, db *gorm.DB, b *blobstore.Store, p *auth.Principal, sid, taskID, name string, data []byte) (File, error) {
	sum := sha256.Sum256(data)
	identity := fmt.Sprintf("%s/%s/%s/%s/%x", p.OrgID, p.PrincipalID, taskID, name, sum)
	var existing File
	if e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "artifact_key", Value: identity}).Limit(1).Find(&existing).Error; e != nil {
		return existing, e
	}
	if existing.ID != "" {
		return existing, nil
	}
	key, size, e := b.PutBytes(ctx, blobstore.Scope{OrgID: p.OrgID, OwnerID: p.PrincipalID}, data)
	if e != nil {
		return existing, e
	}
	f := File{
		ID:          xid.New("file"),
		OrgID:       p.OrgID,
		OwnerID:     p.PrincipalID,
		SessionID:   sid,
		TaskID:      taskID,
		Name:        path.Base(name),
		Path:        name,
		MIME:        http.DetectContentType(data),
		BlobKey:     key,
		Size:        size,
		ArtifactKey: &identity,
	}
	if e = db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "artifact_key"}}, DoNothing: true}).Create(&f).Error; e != nil {
		return f, e
	}
	f = File{}
	e = auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "artifact_key", Value: identity}).Take(&f).Error
	return f, e
}

// GetPinned resolves a previously admitted session input after source deletion.
// Content is immutable; deletion prevents new admissions, not existing references.
func GetPinned(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (File, error) {
	var f File
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&f).Error
	return f, e
}
