package memory

import (
	"context"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

func setup(t *testing.T) (*gorm.DB, *auth.Principal) {
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
	if e = db.AutoMigrate(append(auth.Models(), Models()...)...); e != nil {
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
	t.Cleanup(func() {
		db.Where("org_id = ?", p.OrgID).Delete(&Store{})
	})
	return db, p
}

func TestContentLimitMatchesToolLimit(t *testing.T) {
	db, p := setup(t)
	s, e := Create(context.Background(), db, p, "store")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Write(context.Background(), db, p, s.ID, "big.md", strings.Repeat("x", contentLimitBytes+1), "", 0); e == nil || !strings.Contains(e.Error(), "1 MiB") {
		t.Fatalf("oversized entry accepted: %v", e)
	}
	if _, e = Write(context.Background(), db, p, s.ID, "big.md", strings.Repeat("x", contentLimitBytes), "", 0); e != nil {
		t.Fatalf("entry at the limit rejected: %v", e)
	}
}

func TestStoreLimit(t *testing.T) {
	db, p := setup(t)
	old := storeLimit
	storeLimit = 3
	defer func() { storeLimit = old }()
	for i := 0; i < 3; i++ {
		if _, e := Create(context.Background(), db, p, "store"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Create(context.Background(), db, p, "one too many"); e == nil || !strings.Contains(e.Error(), "memory stores") {
		t.Fatalf("store limit not enforced: %v", e)
	}
}

func TestEntryLimit(t *testing.T) {
	db, p := setup(t)
	old := entryLimit
	entryLimit = 3
	defer func() { entryLimit = old }()
	s, e := Create(context.Background(), db, p, "store")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if _, e = Write(context.Background(), db, p, s.ID, "p"+strings.Repeat("a", i)+"th.md", "v", "", 0); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = Write(context.Background(), db, p, s.ID, "another.md", "v", "", 0); e == nil || !strings.Contains(e.Error(), "entries") {
		t.Fatalf("entry limit not enforced: %v", e)
	}
	// Updating an existing path stays allowed at the limit.
	if _, e = Write(context.Background(), db, p, s.ID, "path.md", "v2", "", 1); e != nil {
		t.Fatalf("update at the limit rejected: %v", e)
	}
}

func TestRevisionAndConflictPruning(t *testing.T) {
	db, p := setup(t)
	old := historyKeep
	historyKeep = 3
	defer func() { historyKeep = old }()
	s, e := Create(context.Background(), db, p, "store")
	if e != nil {
		t.Fatal(e)
	}
	version := int64(0)
	for i := 0; i < 6; i++ {
		row, e := Write(context.Background(), db, p, s.ID, "log.md", "v", "", int(version))
		if e != nil {
			t.Fatal(e)
		}
		version = int64(row.Version)
	}
	var revisions int64
	if e = db.Model(&Revision{}).Where("store_id = ? AND path = ?", s.ID, "log.md").Count(&revisions).Error; e != nil {
		t.Fatal(e)
	}
	if revisions != int64(historyKeep) {
		t.Fatalf("revisions retained: %d", revisions)
	}
	var kept []Revision
	if e = db.Where("store_id = ? AND path = ?", s.ID, "log.md").Order("version").Find(&kept).Error; e != nil {
		t.Fatal(e)
	}
	for i, r := range kept {
		if int64(r.Version) != version-int64(historyKeep)+1+int64(i) {
			t.Fatalf("pruned the wrong revisions: %+v", kept)
		}
	}
	for i := 0; i < 5; i++ {
		if _, e = Write(context.Background(), db, p, s.ID, "log.md", "late", "", 0); e == nil {
			t.Fatal("stale version accepted")
		}
	}
	var conflicts int64
	if e = db.Model(&Conflict{}).Where("store_id = ? AND path = ?", s.ID, "log.md").Count(&conflicts).Error; e != nil {
		t.Fatal(e)
	}
	if conflicts != int64(historyKeep) {
		t.Fatalf("conflicts retained: %d", conflicts)
	}
}
