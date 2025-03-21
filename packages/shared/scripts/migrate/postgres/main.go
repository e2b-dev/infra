package main

import (
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
	log.Printf("Migration direction: %s\n", *direction)

	version, dirty, err := migrator.Version()
	if err == nil {
		log.Printf("Current version: %d, dirty: %t\n", version, dirty)
	} else {
		log.Printf("Failed to get current version: %v\n", err)
	}

	if *direction == "up" {
		err = migrator.Up()
	} else {
		err = migrator.Down()
	}

	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	version, dirty, err = migrator.Version()
	if err == nil {
		log.Printf("Final version: %d, dirty: %t\n", version, dirty)
		log.Printf("Migration completed successfully.")
	} else {
		log.Printf("Failed to get final version: %v\n", err)
		log.Printf("Migration failed.")
	}
}
