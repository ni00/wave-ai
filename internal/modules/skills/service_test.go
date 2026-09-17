package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestUnpackRejectsUnsafeArchives(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		mode       os.FileMode
	}{{"traversal", "../outside", 0644}, {"symlink", "link", os.ModeSymlink | 0777}, {"absolute", "/outside", 0644}, {"duplicate", "SKILL.md", 0644}} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			f, _ := z.Create("SKILL.md")
			f.Write([]byte("instructions"))
			h := &zip.FileHeader{Name: tc.path}
			h.SetMode(tc.mode)
			f, _ = z.CreateHeader(h)
			f.Write([]byte("data"))
			z.Close()
			if _, e := Unpack(b.Bytes()); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("SKILL.md")
	f.Write([]byte("instructions"))
	z.Close()
	if files, e := Unpack(b.Bytes()); e != nil || string(files["SKILL.md"]) != "instructions" {
		t.Fatal(files, e)
	}
}

func TestSkillMetadata(t *testing.T) {
	for _, raw := range []string{"---\nname: search\ndescription: Search project documentation\n---\n# Skill", "---\r\nname: search\r\ndescription: >\r\n  Search project\r\n  documentation\r\n---\r\n# Skill"} {
		meta, e := ParseMetadata([]byte(raw))
		if e != nil || meta.Name != "search" || !strings.Contains(meta.Description, "documentation") {
			t.Fatal(meta, e)
		}
	}
	for _, raw := range []string{"# no metadata", "---\nname: test\n---\nbody", "---\nname: [bad]\ndescription: test\n---", "---\nname: test\ndescription: " + strings.Repeat("x", 2049) + "\n---"} {
		if _, e := ParseMetadata([]byte(raw)); e == nil {
			t.Fatal("accepted invalid metadata")
		}
	}
}

func TestConcurrentUploadsShareOwnerQuota(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	db, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	store, e := blobstore.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e := db.AutoMigrate(append(auth.Models(), &Skill{})...); e != nil {
		t.Fatal(e)
	}
	key, e := auth.Bootstrap(ctx, db, xid.New("org"), "admin", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	p, e := auth.Authenticate(ctx, db, key)
	if e != nil {
		t.Fatal(e)
	}
	old := ownerQuotaBytes
	ownerQuotaBytes = 64
	defer func() { ownerQuotaBytes = old }()

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, e := Create(ctx, db, store, p, Metadata{Name: "test", Description: "test"}, []byte(strings.Repeat("x", 40)))
			results <- e
		})
	}
	close(start)
	workers.Wait()
	close(results)

	succeeded, rejected := 0, 0
	for e := range results {
		switch {
		case e == nil:
			succeeded++
		case strings.Contains(e.Error(), "quota"):
			rejected++
		default:
			t.Fatalf("unexpected upload error: %v", e)
		}
	}
	used, e := OwnerBytes(ctx, db, p)
	if e != nil || succeeded != 1 || rejected != 1 || used != 40 {
		t.Fatalf("success=%d quota_rejections=%d used=%d error=%v", succeeded, rejected, used, e)
	}
}
