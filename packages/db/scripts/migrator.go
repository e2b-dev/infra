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
	trackingTable = "_migrations"
	migrationsDir = "./migrations"
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
		log.Fatalf("failed to create session locker: %v", err)
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
		fmt.Printf("%s %s (%s)\n", res.Direction, res.Source, res.Duration)
	}

	fmt.Println("Migrations applied successfully.")
}
