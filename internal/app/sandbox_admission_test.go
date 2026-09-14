package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestImpossibleSandboxAndUnpreparedCancellation(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	if err := Init(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	key, err := auth.Bootstrap(ctx, db, xid.New("org"), "test", "test", auth.ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	p, err := auth.Authenticate(ctx, db, key)
	if err != nil {
		t.Fatal(err)
	}
	env := environments.Environment{ID: xid.New("env"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "large", SandboxProfile: "large"}
	if err := db.Create(&env).Error; err != nil {
		t.Fatal(err)
	}
	s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID, EnvironmentID: env.ID}
	a := &App{DB: db, Sandbox: sandbox.NewSbx(sandbox.SbxOptions{Store: db, Image: "test", BaseURL: "http://127.0.0.1:1", MemoryBudgetMiB: 1024})}
	task := execution.Task{ID: xid.New("task"), RootID: xid.New("root")}
	if err := db.Transaction(func(tx *gorm.DB) error {
		ok, err := a.admitSandbox(tx, s, &task)
		if !ok && err == nil {
			t.Error("impossible task queued forever")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if task.PendingFinish != "failed" || !strings.Contains(task.Error, "budget") {
		t.Fatalf("invalid outcome: %+v", task)
	}
	// No sandbox RPC may occur when finalizing a task that never prepared.
	if err := a.finish(ctx, s, task); err != nil {
		t.Fatal(err)
	}
	env.SandboxProfile = "default"
	if err := db.Save(&env).Error; err != nil {
		t.Fatal(err)
	}
	task.PendingFinish = ""
	defer db.Where("session_id = ?", s.ID).Delete(&sandbox.Record{})
	if err := db.Transaction(func(tx *gorm.DB) error {
		ok, err := a.admitSandbox(tx, s, &task)
		if !ok && err == nil {
			t.Error("small task rejected")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Cancellation after reservation but before prepare releases it without boot.
	if err := a.finish(ctx, s, task); err != nil {
		t.Fatal(err)
	}
	var record sandbox.Record
	if err := db.Where("session_id = ?", s.ID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.State != "stopped" || record.BackendID != "" {
		t.Fatalf("reservation leaked: %+v", record)
	}
	// An unavailable engine after admission leaves preparation incomplete.
	// Finalization must stop the reservation, never attempt Ensure again.
	if err := db.Transaction(func(tx *gorm.DB) error { _, err := a.admitSandbox(tx, s, &task); return err }); err != nil {
		t.Fatal(err)
	}
	ws := Workspace{SessionID: s.ID, RootID: task.RootID, State: "preparing"}
	if err := db.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Where("session_id = ?", s.ID).Delete(&Workspace{})
	if err := a.finish(ctx, s, task); err != nil {
		t.Fatalf("failed preparation retried unavailable engine: %v", err)
	}
}
