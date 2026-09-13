package auth

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set WAVE_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.AutoMigrate(Models()...); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestConcurrentBootstrapAndRevocation(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name := "test-" + newID()
	const n = 12
	keys := make(chan string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			key, err := Bootstrap(ctx, db, name, "admin", "test", ScopeAPI)
			keys <- key
			errs <- err
		})
	}
	wg.Wait()
	close(keys)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *Principal
	for key := range keys {
		p, err := Authenticate(ctx, db, key)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = p
		}
		if p.OrgID != first.OrgID || p.PrincipalID != first.PrincipalID {
			t.Fatal("concurrent bootstrap split the identity")
		}
	}
	t.Cleanup(func() {
		db.Where(clause.Eq{Column: "org_id", Value: first.OrgID}).Delete(&APIKey{})
		db.Where(clause.Eq{Column: "org_id", Value: first.OrgID}).Delete(&Identity{})
		db.Where(clause.Eq{Column: "id", Value: first.OrgID}).Delete(&Organization{})
	})
	var count int64
	if err := db.Model(&APIKey{}).Where(clause.Eq{Column: "org_id", Value: first.OrgID}).Count(&count).Error; err != nil || count != n {
		t.Fatalf("keys=%d err=%v", count, err)
	}
	raw, err := Bootstrap(ctx, db, name, "admin", "revoked", ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&APIKey{}).Where(clause.Eq{Column: "key_hash", Value: hashKey(raw)}).Update("revoked_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, db, raw); err == nil {
		t.Fatal("revoked key accepted")
	}
	if _, err := Authenticate(ctx, db, "invalid"); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestBootstrapRollsBackAndNeverReturnsUncommittedKey(t *testing.T) {
	db := testDB(t)
	failure := errors.New("injected key persistence failure")
	const callback = "test:fail_key"
	if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "api_keys" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove(callback)
	name := "rollback-" + newID()
	key, err := Bootstrap(context.Background(), db, name, "admin", "test", ScopeAPI)
	if !errors.Is(err, failure) || key != "" {
		t.Fatalf("key returned=%v err=%v", key != "", err)
	}
	var count int64
	if err := db.Model(&Organization{}).Where(clause.Eq{Column: "name", Value: name}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial organization persisted: count=%d err=%v", count, err)
	}
}

func TestBootstrapValidatesBeforePersistence(t *testing.T) {
	for _, args := range [][3]string{{"", "user", ScopeAPI}, {"org", " ", ScopeAPI}, {"org", "user", "unknown"}} {
		key, err := Bootstrap(context.Background(), nil, args[0], args[1], "test", args[2])
		if err == nil || key != "" {
			t.Fatal("invalid input accepted")
		}
	}
}
