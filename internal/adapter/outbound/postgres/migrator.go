package postgres

import (
	"database/sql"
	"errors"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"api/migrations"
)

type Migrator struct {
	m *migrate.Migrate
}

func NewMigrator(dsn string) (*Migrator, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		_ = driver.Close()
		return nil, err
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = driver.Close()
		return nil, err
	}

	return &Migrator{m: m}, nil
}

// Up applies every pending migration; reports false when there was nothing to apply.
func (m *Migrator) Up() (bool, error) {
	return applied(m.m.Up())
}

// Down rolls back the latest applied migration only.
func (m *Migrator) Down() (bool, error) {
	return applied(m.m.Steps(-1))
}

// Force sets the version without running SQL, to recover from a dirty state after a failed migration.
func (m *Migrator) Force(version int) error {
	return m.m.Force(version)
}

// Version returns the current version and whether the last migration failed halfway.
func (m *Migrator) Version() (uint, bool, error) {
	version, dirty, err := m.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

func (m *Migrator) Close() error {
	sourceErr, dbErr := m.m.Close()
	return errors.Join(sourceErr, dbErr)
}

func applied(err error) (bool, error) {
	if errors.Is(err, migrate.ErrNoChange) {
		return false, nil
	}
	return err == nil, err
}
