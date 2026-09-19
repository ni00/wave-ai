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
	opts := ContainerOptions{Backend: "gvisor", Store: db, CPUs: 1, MemoryMiB: 1024, Image: "test-image", MaxRunning: 2, MemoryBudgetMiB: 3072}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			box := capacityContainer(t, opts)
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
	box := capacityContainer(t, opts)
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
	box = capacityContainer(t, opts)
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
	box = capacityContainer(t, opts)
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
	if err := db.Transaction(func(tx *gorm.DB) error {
		return capacityContainer(t, opts).Reserve(tx, prefix+"unknown", Resources{1, 512})
	}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("unknown allocation released: %v", err)
	}
}

func TestReservationRollsBackWithClaim(t *testing.T) {
	db, prefix := capacityDB(t)
	box := capacityContainer(t, ContainerOptions{Backend: "gvisor", Store: db, Image: "test", MaxRunning: 1, MemoryBudgetMiB: 1024})
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

func advisoryLocks(tx *gorm.DB) int64 {
	var n int64
	tx.Raw("SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()").Scan(&n)
	return n
}

func TestReserveLiveRecordSkipsAdvisoryLock(t *testing.T) {
	db, prefix := capacityDB(t)
	box := capacityContainer(t, ContainerOptions{Backend: "gvisor", Store: db, Image: "test-image", MaxRunning: 2, MemoryBudgetMiB: 2048})
	sid := prefix + "-live"
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, sid, Resources{1, 512}) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&Record{}).Where("session_id = ?", sid).Update("state", "running").Error; err != nil {
		t.Fatal(err)
	}
	// A live reservation re-admits without the DB-wide advisory lock: the
	// count is observed inside the same transaction, before the xact lock
	// would be released.
	var locks int64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := box.Reserve(tx, sid, Resources{1, 512}); err != nil {
			return err
		}
		locks = advisoryLocks(tx)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if locks != 0 {
		t.Fatalf("live reservation took %d advisory locks, want 0", locks)
	}
	// A stopped record still converts to reserved while holding the lock.
	if err := db.Model(&Record{}).Where("session_id = ?", sid).Update("state", "stopped").Error; err != nil {
		t.Fatal(err)
	}
	locks = 0
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := box.Reserve(tx, sid, Resources{1, 512}); err != nil {
			return err
		}
		locks = advisoryLocks(tx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if locks != 1 {
		t.Fatalf("stopped conversion took %d advisory locks, want 1", locks)
	}
	var r Record
	if err := db.Where("session_id = ?", sid).Take(&r).Error; err != nil {
		t.Fatal(err)
	}
	if r.State != "reserved" {
		t.Fatalf("conversion left state %q, want reserved", r.State)
	}
}

func TestStoppedConversionAdmissionIsSerialized(t *testing.T) {
	db, prefix := capacityDB(t)
	opts := ContainerOptions{Backend: "gvisor", Store: db, Image: "test-image", MaxRunning: 2, MemoryBudgetMiB: 4096}
	box := capacityContainer(t, opts)
	for _, sid := range []string{prefix + "-a", prefix + "-b"} {
		if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, sid, Resources{1, 1024}) }); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&Record{}).Where("session_id = ?", sid).Update("state", "stopped").Error; err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent wake-ups of stopped sandboxes race fresh admissions; the
	// advisory lock must keep the shared budget exact regardless of order.
	var wg sync.WaitGroup
	for _, sid := range []string{prefix + "-a", prefix + "-b", prefix + "-c", prefix + "-d"} {
		for range 8 {
			wg.Go(func() {
				err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, sid, Resources{1, 1024}) })
				if err != nil && !errors.Is(err, ErrCapacity) {
					t.Error(err)
				}
			})
		}
	}
	wg.Wait()
	var live int64
	if err := db.Model(&Record{}).Where("session_id LIKE ? AND state <> 'stopped'", prefix+"-%").Count(&live).Error; err != nil {
		t.Fatal(err)
	}
	if live != int64(opts.MaxRunning) {
		t.Fatalf("admitted %d live sessions, want exactly the budget of %d", live, opts.MaxRunning)
	}
}

func capacityContainer(t *testing.T, opts ContainerOptions) *Container {
	t.Helper()
	c, err := NewContainer(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
