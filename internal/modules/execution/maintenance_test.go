package execution

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestPruneAuxBatchBoundsTiedTimestamps(t *testing.T) {
	db, p, s := setup(t)
	if e := db.AutoMigrate(&sandbox.Process{}); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	cutoff := time.Now().Add(-24 * time.Hour)
	operation := xid.New("prune")
	receipts := make([]Receipt, 601)
	processes := make([]sandbox.Process, 601)
	for i := range receipts {
		key := fmt.Sprintf("%04d", i)
		receipts[i] = Receipt{PrincipalID: p.PrincipalID, Operation: operation, Key: key, CreatedAt: old}
		processes[i] = sandbox.Process{SessionID: s.ID, RequestID: key, Digest: "test", State: "completed", CreatedAt: old}
	}
	if e := db.CreateInBatches(receipts, 100).Error; e != nil {
		t.Fatal(e)
	}
	if e := db.CreateInBatches(processes, 100).Error; e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Where(clause.Eq{Column: "operation", Value: operation}).Delete(&Receipt{})
		db.Where(clause.Eq{Column: "session_id", Value: s.ID}).Delete(&sandbox.Process{})
	})

	for _, tc := range []struct {
		name  string
		model any
		where clause.Expression
	}{
		{"receipt", &Receipt{}, clause.Eq{Column: "operation", Value: operation}},
		{"process", &sandbox.Process{}, clause.Eq{Column: "session_id", Value: s.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, want := range []int64{500, 101, 0} {
				got, e := pruneAuxBatch(ctx, db, tc.model, cutoff)
				if e != nil {
					t.Fatal(e)
				}
				if got != want {
					t.Fatalf("deleted %d rows, want %d", got, want)
				}
				var remaining int64
				if e := db.Model(tc.model).Where(tc.where).Count(&remaining).Error; e != nil {
					t.Fatal(e)
				}
				wantRemaining := []int64{101, 0, 0}[i]
				if remaining != wantRemaining {
					t.Fatalf("remaining %d rows, want %d", remaining, wantRemaining)
				}
			}
		})
	}
}
