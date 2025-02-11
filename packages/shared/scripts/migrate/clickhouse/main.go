package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	ch "github.com/e2b-dev/infra/packages/shared/pkg/clickhouse"
)

func main() {
	direction := flag.String("direction", "up", "Migration direction (up/down)")
	flag.Parse()

	var (
		connectionString = os.Getenv("CLICKHOUSE_CONNECTION_STRING")
		username         = os.Getenv("CLICKHOUSE_USERNAME")
		password         = os.Getenv("CLICKHOUSE_PASSWORD")
		database         = os.Getenv("CLICKHOUSE_DATABASE")
	)
	// Execute the migration
	migrater, err := ch.NewMigrator(
		connectionString,
		username,
		password,
		database,
	)

	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	if *direction == "up" {
		err = migrater.Up()
	} else {
		err = migrater.Down()
	}

	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	fmt.Println("Migration completed successfully.")
}
