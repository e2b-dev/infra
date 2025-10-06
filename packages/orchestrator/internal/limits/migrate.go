package limits

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*
var migrationsFS embed.FS

func Migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}

	if err := goose.RunContext(ctx, "up", db, "migrations"); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}
	
	return nil
}
