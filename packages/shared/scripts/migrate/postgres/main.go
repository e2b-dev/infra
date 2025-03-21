package main

import (
	"errors"
	"flag"
	"log"
	"os"

	_ "github.com/lib/pq"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

func main() {
	direction := flag.String("direction", "up", "Migration direction (up/down)")
	flag.Parse()

	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	if connectionString == "" {
		log.Fatalf("POSTGRES_CONNECTION_STRING is not set")
	}

	// Execute the migration
	migrator, err := db.NewMigrator(connectionString)
	if err != nil {
		log.Fatalf("Failed to create migrator: %v", err)
	}
	defer migrator.Close()

	version, dirty, err := migrator.Version()
	if errors.Is(err, db.ErrNoVersion) {
		log.Printf("No migration version found, initializing to the latest\n")
	} else if err == nil {
		log.Printf("Current version: %d, dirty: %t\n", version, dirty)
	} else {
		log.Fatalf("Failed to get current version: %v", err)
	}

	log.Printf("Migration direction: %s\n", *direction)
	if *direction == "up" {
		err = migrator.Up()
	} else {
		err = migrator.Down()
	}

	if errors.Is(err, db.ErrNoChange) {
		log.Printf("Latest version already applied\n")
	} else if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	version, dirty, err = migrator.Version()
	if err == nil {
		log.Printf("Final version: %d, dirty: %t\n", version, dirty)
		log.Printf("Migration completed successfully.")
	} else {
		log.Printf("Failed to get final version: %v\n", err)
		log.Fatalf("Migration failed.")
	}
}
