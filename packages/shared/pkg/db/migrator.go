package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Thin wrapper around the migrate package to make it easier to use.

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DatabaseMigrator struct {
	m *migrate.Migrate
}

func (dm *DatabaseMigrator) Up() error {
	return dm.m.Up()
}

func (dm *DatabaseMigrator) Down() error {
	return dm.m.Down()
}

func (dm *DatabaseMigrator) Version() (uint, bool, error) {
	return dm.m.Version()
}

func (dm *DatabaseMigrator) To(version uint) error {
	return dm.m.Migrate(version)
}

func (dm *DatabaseMigrator) Force(version int) error {
	return dm.m.Force(version)
}

func (dm *DatabaseMigrator) List() ([]string, error) {
	dirEntries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	migrationFiles := make([]string, 0)
	for _, entry := range dirEntries {
		migrationFiles = append(migrationFiles, entry.Name())
	}
	return migrationFiles, nil
}

func (dm *DatabaseMigrator) Close() error {
	err1, err2 := dm.m.Close()
	if err1 != nil || err2 != nil {
		return fmt.Errorf("source close error: %v, driver close error: %v", err1, err2)
	}
	return nil
}

func (dm *DatabaseMigrator) SetLogger(logger migrate.Logger) {
	dm.m.Log = logger
}

func NewMigrator(connectionString string) (*DatabaseMigrator, error) {
	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to open database migrations iofs: %w", err)
	}

	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres driver instance: %w", err)
	}

	m, err := migrate.NewWithInstance(
		"iofs",
		d,
		"postgres",
		driver,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres migrate instance: %w", err)
	}

	return &DatabaseMigrator{
		m: m,
	}, nil
}
