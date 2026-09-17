package app

import (
	"context"
	"fmt"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
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
		if err := VerifySchema(db); err != nil {
			return err
		}
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.AutoMigrate(Models()...)
	})
}

// VerifySchema reports whether an existing database matches the models of this
// build. init-db never upgrades an existing database, so a build whose models
// drifted from the stored schema must fail startup with a clear error instead
// of failing every dependent request.
func VerifySchema(db *gorm.DB) error {
	for _, model := range Models() {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("database schema mismatch: table for %T does not exist; the database was created by a different build and init-db does not upgrade existing databases; initialize a new database per the deployment guide", model)
		}
		s, err := schema.Parse(model, &sync.Map{}, db.NamingStrategy)
		if err != nil {
			return fmt.Errorf("database schema check: %w", err)
		}
		cols, err := db.Migrator().ColumnTypes(model)
		if err != nil {
			return fmt.Errorf("database schema check: %w", err)
		}
		actual := make(map[string]bool, len(cols))
		for _, c := range cols {
			actual[c.Name()] = true
		}
		missing := []string{}
		for _, f := range s.Fields {
			if f.DBName == "" || actual[f.DBName] {
				continue
			}
			missing = append(missing, f.DBName)
		}
		if len(missing) > 0 {
			return fmt.Errorf("database schema mismatch: table %s lacks columns %v required by this build; the database was created by a different build and init-db does not upgrade existing databases; initialize a new database per the deployment guide", s.Table, missing)
		}
	}
	return nil
}
