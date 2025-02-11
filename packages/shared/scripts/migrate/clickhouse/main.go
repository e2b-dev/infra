package main

import (
	"fmt"
	"log"
	"os"

	ch "github.com/e2b-dev/infra/packages/shared/pkg/clickhouse"
)

func main() {
	connectionString := os.Getenv("CLICKHOUSE_CONNECTION_STRING")
	username := os.Getenv("CLICKHOUSE_USERNAME")
	password := os.Getenv("CLICKHOUSE_PASSWORD")
	database := os.Getenv("CLICKHOUSE_DATABASE")

	if connectionString == "" {
		log.Fatalf("CLICKHOUSE_CONNECTION_STRING is not set")
	}

	// Execute the migration
	migrater, err := ch.NewMigrator(connectionString, username, password, database)
	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	err = migrater.Up()
	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	fmt.Println("Migration completed successfully.")
}
