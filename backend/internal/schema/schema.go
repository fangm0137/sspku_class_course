package schema

import (
	"context"
	"errors"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/migrations"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Up applies pending migrations; false means the schema was already current.
func Up(cfg config.Database) (bool, error) {
	pool, err := db.OpenForMigration(cfg)
	if err != nil {
		return false, err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		return false, errors.New("migration database unavailable; check DB_* configuration")
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return false, errors.New("cannot load embedded migrations")
	}
	driver, err := migratemysql.WithInstance(pool, &migratemysql.Config{})
	if err != nil {
		source.Close()
		return false, errors.New("cannot initialize migration metadata")
	}
	m, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		source.Close()
		driver.Close()
		return false, errors.New("cannot initialize migration runner")
	}
	defer m.Close()
	err = m.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("migration failed; inspect schema_migrations and server diagnostics; do not force a dirty version")
	}
	return true, nil
}
