package execution

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestCapacityDoesNotBlockUnrelatedTasksOrCancellation(t *testing.T) {
	db, p, s := setup(t)
	a := agent(t, db, p, "", nil)
	blocked := task(t, db, p, s, a)
	other := s
	other.ID = xid.New("session")
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Model(&Task{}).Where("session_id = ?", other.ID).Update("state", "canceled")
	allowed := task(t, db, p, other, a)
	admit := func(_ *gorm.DB, session Session, _ *Task) (bool, error) { return session.ID != s.ID, nil }
	claimed, err := claimWithAdmission(context.Background(), db, "worker", time.Second, admit)
	if err != nil || claimed == nil || claimed.ID != allowed.ID {
		t.Fatalf("unrelated task blocked: %+v %v", claimed, err)
	}
	var pending Task
	if err := db.Where("id = ?", blocked.ID).Take(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if pending.State != "queued" || pending.StartedAt != nil || pending.Owner != "" {
		t.Fatalf("capacity wait started task: %+v", pending)
	}
	db.Model(&Task{}).Where("id = ?", allowed.ID).Update("state", "canceled")
	if err := Cancel(context.Background(), db, p, blocked.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = claimWithAdmission(context.Background(), db, "worker", time.Second, admit)
	if err != nil || claimed == nil || claimed.ID != blocked.ID {
		t.Fatalf("capacity blocked cancellation: %+v %v", claimed, err)
	}
}
