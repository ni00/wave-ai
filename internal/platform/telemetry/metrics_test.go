package telemetry

import (
	"context"
	"errors"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestHistogramMerge(t *testing.T) {
	var a, b Distribution
	for i := 1; i <= 1000; i++ {
		if i <= 950 {
			a.Add(float64(i), true)
		} else {
			b.Add(float64(i), true)
		}
	}
	a.Merge(b)
	for _, q := range []float64{.5, .95, .99} {
		got := a.Quantile(q)
		want := 1000 * q
		if got == nil || math.Abs(*got-want)/want > .02 {
			t.Fatalf("q=%v got=%v", q, got)
		}
	}
	if a.Count != 1000 || a.Sum != 500500 {
		t.Fatal(a)
	}
	var z Distribution
	z.Add(0, true)
	if z.Quantile(.99) == nil || *z.Quantile(.99) != 0 {
		t.Fatal("zero lost")
	}
	if (Distribution{}).Quantile(.95) != nil {
		t.Fatal("unknown must remain null")
	}
}
func testDB(t *testing.T) *gorm.DB {
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
	if e = db.AutoMigrate(Models()...); e != nil {
		t.Fatal(e)
	}
	return db
}
func TestTransactionalConcurrentReduction(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	p := &auth.Principal{OrgID: xid.New("org"), PrincipalID: xid.New("owner")}
	at := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	rollback := errors.New("rollback")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if e := Enqueue(tx, p.OrgID, p.PrincipalID, at, map[string]float64{"tasks.completed": 99}); e != nil {
			return e
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	for i := 1; i <= 512; i++ {
		if err := Enqueue(db, p.OrgID, p.PrincipalID, at.Add(time.Duration(i%3)*time.Minute), map[string]float64{"tasks.completed": 1, "tasks.succeeded": 1, "task.duration": float64(i), "tokens.input": 10, "task_id": 100, "model.first_token": math.NaN()}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, e := Reduce(ctx, db)
				if e != nil {
					t.Error(e)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	result, e := Query(ctx, db, p, at, at.Add(4*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if result.Totals["tasks.completed"].Sum != 512 || result.Totals["tokens.input"].Sum != 5120 || len(result.Totals) != 4 || result.PendingSince != nil {
		t.Fatalf("bad totals %+v", result)
	}
	if got := result.Totals["task.duration"].P95; got == nil || math.Abs(*got-487) > 10 {
		t.Fatalf("bad merged percentile %v", got)
	}
	// Hour rollup must contain the same samples, and re-running cannot duplicate.
	if _, e = Reduce(ctx, db); e != nil {
		t.Fatal(e)
	}
	wide, e := Query(ctx, db, p, at.Add(-25*time.Hour), at.Add(4*time.Minute))
	if e != nil || wide.Totals["tasks.completed"].Sum != 512 {
		t.Fatalf("hour rollup %+v %v", wide, e)
	}
	peer := *p
	peer.PrincipalID = xid.New("owner")
	other, e := Query(ctx, db, &peer, at, at.Add(time.Hour))
	if e != nil || len(other.Totals) != 0 {
		t.Fatalf("owner leak %+v %v", other, e)
	}
	// A failed reducer transaction must roll back both its buckets and deletion.
	if e = Enqueue(db, p.OrgID, p.PrincipalID, at, map[string]float64{"tasks.completed": 1}); e != nil {
		t.Fatal(e)
	}
	name := "monitor_rollback_" + xid.New("test")
	db.Callback().Delete().Before("gorm:delete").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "monitor_samples" {
			tx.AddError(rollback)
		}
	})
	_, e = Reduce(ctx, db)
	db.Callback().Delete().Remove(name)
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	result, e = Query(ctx, db, p, at, at.Add(time.Hour))
	if e != nil || result.Totals["tasks.completed"].Sum != 512 || result.PendingSince == nil {
		t.Fatalf("failed transaction leaked %+v %v", result, e)
	}
	if _, e = Reduce(ctx, db); e != nil {
		t.Fatal(e)
	}
	result, e = Query(ctx, db, p, at, at.Add(time.Hour))
	if e != nil || result.Totals["tasks.completed"].Sum != 513 {
		t.Fatal(result, e)
	}
}
func TestRetentionAndLogSink(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id := xid.New("owner")
	now := time.Now().UTC()
	sink := NewLogSink(db)
	sink.Write(observe.Log{OrgID: id, OwnerID: id, CreatedAt: now, Level: "error", Module: "model", Message: "model.finished", Attributes: map[string]any{"state": "failed"}})
	sink.Close()
	var rows []observe.Log
	if e := db.Where(clause.Eq{Column: "owner_id", Value: id}).Find(&rows).Error; e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	old := rows[0]
	old.ID = xid.New("log")
	old.CreatedAt = now.Add(-8 * 24 * time.Hour)
	if e := db.Create(&old).Error; e != nil {
		t.Fatal(e)
	}
	if e := Prune(ctx, db, now); e != nil {
		t.Fatal(e)
	}
	var count int64
	db.Model(&observe.Log{}).Where(clause.Eq{Column: "owner_id", Value: id}).Count(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
