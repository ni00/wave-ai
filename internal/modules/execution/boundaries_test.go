package execution

import (
	"context"
	"gorm.io/gorm"
	"testing"
	"time"
	"wave-ai.local/wave/internal/adapters/modelclient"
)

func TestRegressionConsumedInputSurvivesBudgetFailure(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(context.Background(), db, "review", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	if e = db.Model(&Task{}).Where(eq("id", x.ID)).Update("used_tokens", x.Budget.MaxTokens).Error; e != nil {
		t.Fatal(e)
	}
	if e = (&Worker{DB: db, Model: fakeModel{}}).step(context.Background(), claim); e != nil {
		t.Fatal(e)
	}
	if e = db.Where(eq("id", x.ID)).Take(&x).Error; e != nil {
		t.Fatal(e)
	}
	var inputs []Input
	if e = db.Where(eq("task_id", x.ID)).Find(&inputs).Error; e != nil {
		t.Fatal(e)
	}
	for _, in := range inputs {
		found := false
		for _, m := range x.Messages {
			if m.Role == "user" && m.Content == in.Text {
				found = true
			}
		}
		if in.Consumed && !found {
			t.Fatalf("input marked consumed but absent from task checkpoint: %q", in.Text)
		}
	}
}
func TestRegressionDurationBudgetAfterPrepare(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	x.Budget.MaxSeconds = 1
	if e := db.Model(&x).Select("budget").Updates(&x).Error; e != nil {
		t.Fatal(e)
	}
	claim, e := Claim(context.Background(), db, "review", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	calls := 0
	w := Worker{DB: db, Prepare: func(context.Context, Session, Task) error { time.Sleep(1200 * time.Millisecond); return nil }, Model: modelFunc(func(c context.Context, r modelclient.Request, d func(modelclient.Delta)) (*modelclient.Final, error) {
		calls++
		return fakeModel{}.Stream(c, r, d)
	})}
	if e = w.step(context.Background(), claim); e != nil {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatalf("made %d model call after Prepare exceeded MaxSeconds", calls)
	}
}
func TestRegressionPendingPrepareDoesNotCountAttempt(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(context.Background(), db, "review", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	w := Worker{DB: db, Prepare: func(context.Context, Session, Task) error { return ErrPending }, Model: fakeModel{}}
	if e = w.step(context.Background(), claim); e != nil {
		t.Fatal(e)
	}
	if e = db.Where(eq("id", x.ID)).Take(&x).Error; e != nil {
		t.Fatal(e)
	}
	if x.Attempts != 0 {
		t.Fatalf("attempts=%d although workspace pending and no model call made", x.Attempts)
	}
}
func TestRegressionRenewalRejectsExpiryDuringLockWait(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	x := task(t, db, p, s, a)
	claim, e := Claim(context.Background(), db, "review", time.Minute)
	if e != nil || claim == nil || claim.ID != x.ID {
		t.Fatalf("claim %v %v", claim, e)
	}
	deadline := time.Now().Add(700 * time.Millisecond)
	if e = db.Model(&Task{}).Where(eq("id", x.ID)).Update("lease_until", deadline).Error; e != nil {
		t.Fatal(e)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	var row Task
	if e = tx.Clauses(lock).Where(eq("id", x.ID)).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{}, 1)
	if e = db.Callback().Update().Before("gorm:update").Register("review:renewal-start", func(tx *gorm.DB) {
		select {
		case started <- struct{}{}:
		default:
		}
	}); e != nil {
		t.Fatal(e)
	}
	defer db.Callback().Update().Remove("review:renewal-start")
	done := make(chan bool, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go func() { done <- (&Worker{DB: db, Lease: time.Minute}).renewLease(ctx, claim, func() {}) }()
	// The old implementation blocks on its locked read before reaching UPDATE;
	// the new implementation reaches UPDATE immediately and then blocks.
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
	}
	time.Sleep(time.Until(deadline) + 200*time.Millisecond)
	if e = tx.Commit().Error; e != nil {
		t.Fatal(e)
	}
	select {
	case ok := <-done:
		if ok {
			t.Fatal("renewal accepted a lease that expired during row-lock wait")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
