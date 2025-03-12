package chdb

import (
	"embed"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/golang-migrate/migrate/v4"
	migch "github.com/golang-migrate/migrate/v4/database/clickhouse"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Thin wrapper around the migrate package to make it easier to use.

//go:embed migrations/*.sql
var migrationsFS embed.FS

type ClickhouseMigrator struct {
	m *migrate.Migrate
}

func (chMig *ClickhouseMigrator) Up() error {
	return chMig.m.Up()
}

func (chMig *ClickhouseMigrator) Down() error {
	return chMig.m.Down()
}

func (chMig *ClickhouseMigrator) Version() (uint, bool, error) {
	return chMig.m.Version()
}

func (chMig *ClickhouseMigrator) To(version uint) error {
	return chMig.m.Migrate(version)
}

func (chMig *ClickhouseMigrator) Force(version int) error {
	return chMig.m.Force(version)
}

func (chMig *ClickhouseMigrator) List() ([]string, error) {
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

func (chMig *ClickhouseMigrator) Close() error {
	err1, err2 := chMig.m.Close()
	if err1 != nil || err2 != nil {
		return fmt.Errorf("source close error: %v, driver close error: %v", err1, err2)
	}
	return nil
}

func (chMig *ClickhouseMigrator) SetLogger(logger migrate.Logger) {
	chMig.m.Log = logger
}

func NewMigrator(config ClickHouseConfig) (*ClickhouseMigrator, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate ClickHouse config: %w", err)
	}

	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to open Clickhouse migrations iofs: %w", err)
	}

	db := clickhouse.OpenDB(&clickhouse.Options{
		Addr:     []string{config.ConnectionString},
		Protocol: clickhouse.Native,
		Auth: clickhouse.Auth{
			Database: config.Database,
			Username: config.Username,
			Password: config.Password,
		},
	})

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS default.schema_migrations (
			version    Int64,
			dirty      UInt8,
			sequence   UInt64
		)
		ENGINE = MergeTree()
		ORDER BY tuple();
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	driver, err := migch.WithInstance(db, &migch.Config{
		DatabaseName: config.Database,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cl,ickhouse driver: %w", err)
	}

	m, err := migrate.NewWithInstance(
		"iofs", d,
		"clickhouse", driver)
	if err != nil {
		return nil, fmt.Errorf("failed to create clickhouse migrate instance: %w", err)
	}

	return &ClickhouseMigrator{
		m: m,
	}, nil
}
