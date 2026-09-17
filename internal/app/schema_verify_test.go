package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"wave-ai.local/wave/internal/platform/database"
	"wave-ai.local/wave/internal/platform/xid"
)

// A database created by an older build must fail startup with a clear error
// instead of serving requests whose claims then fail forever.
func TestVerifySchemaRejectsColumnDrift(t *testing.T) {
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
	name := strings.ToLower(xid.New("verify"))
	if err := admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", name)).Error; err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", name))
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	dsn = u.String()

	if err := Init(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	// Drift a column the way an older schema would: same table, stale layout.
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	dataPool, _ := db.DB()
	defer dataPool.Close()
	if err := db.Exec("ALTER TABLE sandbox_instances RENAME COLUMN workspace_gi_b TO workspace_gib").Error; err != nil {
		t.Fatal(err)
	}

	err = VerifySchema(db)
	if err == nil {
		t.Fatal("expected schema verification to fail on drifted column")
	}
	if !strings.Contains(err.Error(), "workspace_gi_b") || !strings.Contains(err.Error(), "sandbox_instances") {
		t.Fatalf("error should name table and missing column, got: %v", err)
	}

	// Restoring the column layout makes the same database acceptable again.
	if err := db.Exec("ALTER TABLE sandbox_instances RENAME COLUMN workspace_gib TO workspace_gi_b").Error; err != nil {
		t.Fatal(err)
	}
	if err := VerifySchema(db); err != nil {
		t.Fatalf("expected verification to pass after schema restore, got: %v", err)
	}

	// Init on an existing drifted database must not silently accept it either.
	if err := db.Exec("ALTER TABLE sandbox_instances DROP COLUMN workspace_gi_b").Error; err != nil {
		t.Fatal(err)
	}
	if err := Init(ctx, dsn); err == nil {
		t.Fatal("expected init to reject an existing database with missing columns")
	}
}

// VerifySchema must accept the schema init-db creates for this build.
func TestVerifySchemaAcceptsCurrentSchema(t *testing.T) {
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
	name := strings.ToLower(xid.New("verifyok"))
	if err := admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", name)).Error; err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", name))
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
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
	if err := VerifySchema(db); err != nil {
		t.Fatalf("verification rejected current schema: %v", err)
	}
}
