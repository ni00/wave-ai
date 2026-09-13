package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"strings"

	"github.com/goccy/go-yaml"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
)

func Unpack(raw []byte) (map[string][]byte, error) {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return nil, apierr.Invalid("invalid ZIP archive")
	}
	if len(z.File) > 200 {
		return nil, apierr.Invalid("at most 200 skill files")
	}
	out := map[string][]byte{}
	total := 0
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "../") || f.Mode()&os.ModeType != 0 {
			return nil, apierr.Invalid("unsafe skill path")
		}
		if _, exists := out[name]; exists {
			return nil, apierr.Invalid("duplicate skill file")
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(r, (20<<20)+1))
		r.Close()
		if e != nil {
			return nil, e
		}
		total += len(b)
		if total > 20<<20 {
			return nil, apierr.Invalid("skill archive exceeds 20 MiB")
		}
		out[name] = b
	}
	if len(out["SKILL.md"]) == 0 {
		return nil, apierr.Invalid("SKILL.md must be at archive root")
	}
	return out, nil
}
func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Skill, error) {
	var s Skill
	err := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&s).Error
	return s, err
}
func Load(ctx context.Context, db *gorm.DB, b *blobstore.Store, p *auth.Principal, id string) (Skill, map[string][]byte, error) {
	s, e := Get(ctx, db, p, id)
	if e != nil {
		return s, nil, e
	}
	raw, e := b.ReadAll(ctx, blobstore.Scope{OrgID: s.OrgID, OwnerID: s.OwnerID}, s.BlobKey, 24<<20)
	if e != nil {
		return s, nil, e
	}
	items, e := Unpack(raw)
	return s, items, e
}

type Metadata struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

func ParseMetadata(raw []byte) (Metadata, error) {
	var meta Metadata
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return meta, apierr.Invalid("SKILL.md requires YAML name and description")
	}
	head, _, ok := strings.Cut(text[4:], "\n---")
	if !ok || len(head) > 16<<10 {
		return meta, apierr.Invalid("invalid skill frontmatter")
	}
	if e := yaml.Unmarshal([]byte(head), &meta); e != nil {
		return meta, apierr.Invalid("invalid skill metadata")
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	if meta.Name == "" || len(meta.Name) > 128 || strings.ContainsAny(meta.Name, "\r\n") || meta.Description == "" || len(meta.Description) > 2048 {
		return meta, apierr.Invalid("skill name (1–128 bytes) and description (1–2048 bytes) required")
	}
	return meta, nil
}
