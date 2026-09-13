package app

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"wave-ai.local/wave/internal/platform/database"
)

// Init creates the current models in an empty database. Repeated startup is a
// no-op once all application tables exist; it never upgrades or resets data.
func Init(ctx context.Context, dsn string) error {
	db, err := database.Open(ctx, dsn)
	if err != nil {
		return err
	}
	pool, _ := db.DB()
	defer pool.Close()
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		for _, model := range Models() {
			if !db.Migrator().HasTable(model) {
				return fmt.Errorf("database initialization requires an empty database")
			}
		}
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.AutoMigrate(Models()...)
	})
}
