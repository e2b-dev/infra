package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

const (
	trackingTable        = "_migrations"
	migrationsDir        = "./migrations"
	authMigrationVersion = 20000101000000
)

func main() {
	fmt.Printf("Starting migrations...\n")
	ctx := context.Background()

	dbString := os.Getenv("POSTGRES_CONNECTION_STRING")
	if dbString == "" {
		log.Fatal("Database connection string is required. Set POSTGRES_CONNECTION_STRING env var.")
	}

	db, err := sql.Open("postgres", dbString)
	if err != nil {
		log.Fatalf("failed to open DB: %v", err)
	}
	defer func() {
		err := db.Close()
		if err != nil {
			log.Printf("failed to close DB: %v\n", err)
		}
	}()

	// Create a session locking
	sessionLocker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		log.Fatalf("failed to create session locker: %v", err) //nolint:gocritic // no harm in exiting after defer here
	}

	goose.SetTableName(trackingTable)

	version, err := goose.EnsureDBVersion(db)
	if err != nil {
		log.Fatalf("EnsureDBVersion: %v", err)
	}

	fmt.Printf("Current DB version: %d\n", version)
	if version < authMigrationVersion {
		fmt.Println("Creating auth.users table...")
		err = setupAuthSchema(db, version)
		if err != nil {
			log.Fatalf("failed to ensure auth.users table: %v", err)
		}
	}

	// We have to use custom store to use a custom tracking table
	store, err := database.NewStore(goose.DialectPostgres, trackingTable)
	if err != nil {
		log.Fatalf("failed to create database store: %v", err)
	}

	migrationsFS := os.DirFS(migrationsDir)
	provider, err := goose.NewProvider(
		"", // Has to empty when using a custom store
		db,
		migrationsFS,
		goose.WithStore(store),
		goose.WithSessionLocker(sessionLocker),
	)
	if err != nil {
		log.Fatalf("failed to create goose provider: %v", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		log.Fatalf("failed to apply migrations: %v", err)
	}

	for _, res := range results {
		fmt.Printf("Applied migration %s %s (%s)\n", res.Direction, res.Source.Path, res.Duration)
	}

	fmt.Println("Migrations applied successfully.")
}

func setupAuthSchema(db *sql.DB, version int64) error {
	rows, err := db.Query(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'users')`)
	if err != nil {
		return fmt.Errorf("failed to query: %w", err)
	}

	defer func() {
		err = rows.Close()
		if err != nil {
			log.Printf("failed to close rows: %v\n", err)
		}
	}()

	exists := false
	for rows.Next() {
		err = rows.Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to scan: %w", err)
		}
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("failed to finish scanning: %w", err)
	}

	if !exists {
		// Setup auth schema
		_, err = db.Exec(
			`CREATE SCHEMA IF NOT EXISTS auth;`)
		if err != nil {
			return fmt.Errorf("failed to create schema: %w", err)
		}

		// Create authenticated user
		_, err = db.Exec("CREATE ROLE authenticated;")
		if err != nil {
			return fmt.Errorf("failed to create role: %w", err)
		}

		// Create users table
		_, err = db.Exec(
			`CREATE TABLE IF NOT EXISTS auth.users (id uuid NOT NULL DEFAULT gen_random_uuid(),email text NOT NULL, PRIMARY KEY (id));`)
		if err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}

		// Create function to generate a random uuid
		_, err = db.Exec(
			`CREATE FUNCTION auth.uid() RETURNS uuid AS $func$
		BEGIN
			RETURN gen_random_uuid();
		END;
		$func$ LANGUAGE plpgsql;`)
		if err != nil {
			return fmt.Errorf("failed to create function: %w", err)
		}

		// Grant execute permission to authenticated role
		_, err = db.Exec(`GRANT EXECUTE ON FUNCTION auth.uid() TO postgres`)
		if err != nil {
			return fmt.Errorf("failed to grant function: %w", err)
		}
	}

	// Insert migration record
	if version < authMigrationVersion {
		_, err = db.Exec(fmt.Sprintf("INSERT INTO %s (version_id, is_applied) VALUES (%d, true)", trackingTable, authMigrationVersion))
		if err != nil {
			return fmt.Errorf("failed to insert version: %w", err)
		}
	}

	return nil
}
