package files

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

func setup(t *testing.T) (*gorm.DB, *auth.Principal, *blobstore.Store) {
	t.Helper()
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, e := database.Open(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	store, e := blobstore.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(append(auth.Models(), &File{})...); e != nil {
		t.Fatal(e)
	}
	key, e := auth.Bootstrap(context.Background(), db, xid.New("org"), "admin", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	p, e := auth.Authenticate(context.Background(), db, key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Where("org_id = ?", p.OrgID).Delete(&File{}) })
	return db, p, store
}

func TestOwnerByteQuota(t *testing.T) {
	db, p, store := setup(t)
	old := ownerQuotaBytes
	ownerQuotaBytes = 64
	defer func() { ownerQuotaBytes = old }()
	if _, e := Put(context.Background(), db, store, p, "", "a.txt", []byte(strings.Repeat("x", 40))); e != nil {
		t.Fatal(e)
	}
	used, e := OwnerBytes(context.Background(), db, p)
	if e != nil || used != 40 {
		t.Fatalf("used bytes: %d %v", used, e)
	}
	if _, e := Put(context.Background(), db, store, p, "", "b.txt", []byte(strings.Repeat("x", 25))); e == nil || !strings.Contains(e.Error(), "quota") {
		t.Fatalf("quota not enforced: %v", e)
	}
	if _, e := Put(context.Background(), db, store, p, "", "b.txt", []byte(strings.Repeat("x", 24))); e != nil {
		t.Fatalf("write within quota rejected: %v", e)
	}
	// Soft-deleting frees quota without touching the blob store.
	if e := db.Model(&File{}).Where("owner_id = ?", p.PrincipalID).Update("deleted", true).Error; e != nil {
		t.Fatal(e)
	}
	if used, e = OwnerBytes(context.Background(), db, p); e != nil || used != 0 {
		t.Fatalf("delete did not reclaim quota: %d %v", used, e)
	}
}

func TestConcurrentUploadsShareOwnerQuota(t *testing.T) {
	db, p, store := setup(t)
	old := ownerQuotaBytes
	ownerQuotaBytes = 64
	defer func() { ownerQuotaBytes = old }()

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"a.txt", "b.txt"} {
		workers.Go(func() {
			<-start
			_, e := Put(context.Background(), db, store, p, "", name, []byte(strings.Repeat("x", 40)))
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
	used, e := OwnerBytes(context.Background(), db, p)
	if e != nil || succeeded != 1 || rejected != 1 || used != 40 {
		t.Fatalf("success=%d quota_rejections=%d used=%d error=%v", succeeded, rejected, used, e)
	}
}
