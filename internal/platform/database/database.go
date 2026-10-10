// Package database opens the shared ORM pool. It owns no business rules.
package database

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open connects with the default pool limits; see OpenPool.
func Open(ctx context.Context, dsn string) (*gorm.DB, error) {
	return OpenPool(ctx, dsn, 32)
}

// OpenPool connects with a caller-set maximum open connection count. The
// historical default of 32 measured fastest on write-saturated worker hosts;
// read-heavy deployments (for example many polling clients) raise it via
// WAVE_DB_MAX_OPEN_CONNS instead of editing code.
func OpenPool(ctx context.Context, dsn string, maxOpen int) (*gorm.DB, error) {
	// Keep PrepareStmt disabled: it regressed throughput in the measured
	// concurrent workload. Re-benchmark before enabling it; the cause is unverified.
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	// Idle capacity matches the ceiling: under load, a smaller idle pool
	// closes and reopens connections faster than they can be reused. The
	// ceiling trades PostgreSQL scheduling cost against concurrent demand.
	if maxOpen <= 0 {
		maxOpen = 32
	}
	pool.SetMaxOpenConns(maxOpen)
	pool.SetMaxIdleConns(maxOpen)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	// Rotate long-lived connections so failovers and DNS changes cannot
	// strand traffic on a dead peer until the idle timeout fires.
	pool.SetConnMaxLifetime(30 * time.Minute)
	if err = pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}
