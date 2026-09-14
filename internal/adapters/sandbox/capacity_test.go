package sandbox

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

func capacityDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	prefix := xid.New("capacity")
	t.Cleanup(func() { db.Where("session_id LIKE ?", prefix+"%").Delete(&Record{}); pool.Close() })
	if err := db.AutoMigrate(&Record{}); err != nil {
		t.Fatal(err)
	}
	return db, prefix
}

func TestCapacityAcrossReplicas(t *testing.T) {
	db, prefix := capacityDB(t)
	opts := SbxOptions{Store: db, CPUs: 1, MemoryMiB: 1024, Image: "test-image", MaxRunning: 2, MemoryBudgetMiB: 3072}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			box := NewSbx(opts)
			err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix+xid.New("s"), Resources{1, 1024}) })
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrCapacity) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 2 {
		t.Fatalf("admitted %d sessions, want 2", successes.Load())
	}
	var rows []Record
	if err := db.Where("session_id LIKE ?", prefix+"%").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	box := NewSbx(opts)
	// A new provider instance (worker restart) and repeat admission consume no extra slot.
	for _, r := range rows {
		if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, r.SessionID, Resources{2, 2048}) }); err != nil {
			t.Fatal(err)
		}
		if err := box.Stop(context.Background(), r.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	// Retained specifications survive changes to defaults and profile requests.
	opts.Image, opts.MemoryMiB = "replacement-image", 2048
	box = NewSbx(opts)
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, rows[0].SessionID, Resources{2, 2048}) }); err != nil {
		t.Fatal(err)
	}
	var retained Record
	db.Where("session_id = ?", rows[0].SessionID).Take(&retained)
	if retained.MemoryMiB != 1024 || retained.CPUs != 1 || retained.Image != "test-image" {
		t.Fatalf("retained spec changed: %+v", retained)
	}
	if err := box.Stop(context.Background(), retained.SessionID); err != nil {
		t.Fatal(err)
	}
	// Memory budget applies even when there are free count slots.
	opts.MaxRunning, opts.MemoryBudgetMiB = 8, 2048
	box = NewSbx(opts)
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix+"large", Resources{2, 2048}) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix+"extra", Resources{1, 512}) }); !errors.Is(err, ErrCapacity) {
		t.Fatalf("memory overcommit: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix+"impossible", Resources{2, 4096}) }); !errors.Is(err, ErrResources) {
		t.Fatalf("impossible allocation: %v", err)
	}
	// Unknown outcomes continue to consume capacity after a process restart.
	db.Model(&Record{}).Where("session_id = ?", prefix+"large").Update("state", "creating")
	if err := db.Transaction(func(tx *gorm.DB) error { return NewSbx(opts).Reserve(tx, prefix+"unknown", Resources{1, 512}) }); !errors.Is(err, ErrCapacity) {
		t.Fatalf("unknown allocation released: %v", err)
	}
}

func TestReservationRollsBackWithClaim(t *testing.T) {
	db, prefix := capacityDB(t)
	box := NewSbx(SbxOptions{Store: db, Image: "test", MaxRunning: 1, MemoryBudgetMiB: 1024})
	abort := errors.New("claim aborted")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := box.Reserve(tx, prefix+"aborted", Resources{1, 1024}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix+"next", Resources{1, 1024}) }); err != nil {
		t.Fatal(err)
	}
}
