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

func Open(ctx context.Context, dsn string) (*gorm.DB, error) {
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
	// closes and reopens connections faster than they can be reused. Raising
	// the ceiling past 32 measured slower on saturated worker hosts: the extra
	// connections add PostgreSQL scheduling cost without adding CPU.
	pool.SetMaxOpenConns(32)
	pool.SetMaxIdleConns(32)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	if err = pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}
