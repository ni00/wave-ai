package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestDatabaseInitializesFinalSchema(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := admin.DB()
	defer pool.Close()
	schema := strings.ToLower(xid.New("init"))
	if err := admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error; err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema))
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn = u.String()
	if err := Init(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	dataPool, _ := db.DB()
	defer dataPool.Close()
	for _, model := range Models() {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing model table: %T", model)
		}
	}
	for _, field := range []string{"CPUs", "MemoryMiB", "Image", "Backend", "EngineHost", "Runtime", "Network", "StagerID"} {
		if !db.Migrator().HasColumn(&sandbox.Record{}, field) {
			t.Fatalf("missing sandbox field %s", field)
		}
	}
	for _, field := range []string{"SandboxBackend", "SandboxProfile"} {
		if !db.Migrator().HasColumn(&environments.Environment{}, field) {
			t.Fatalf("missing environment field %s", field)
		}
	}
	row := sandbox.Record{SessionID: "retained", Backend: "gvisor", BackendID: "container", EngineHost: "unix:///var/run/docker.sock", Name: "wave-retained", State: "stopped", CPUs: 1, MemoryMiB: 1024, Image: sandbox.DefaultContainerImage}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	// Repeated initialization must retain data and never reset the database.
	if err := Init(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	var retained sandbox.Record
	if err := db.Where("session_id = ?", row.SessionID).Take(&retained).Error; err != nil {
		t.Fatal(err)
	}
	if retained.Backend != row.Backend || retained.BackendID != row.BackendID || retained.CPUs != row.CPUs || retained.MemoryMiB != row.MemoryMiB {
		t.Fatalf("initialization changed data: %+v", retained)
	}
}
