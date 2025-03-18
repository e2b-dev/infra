package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
)

func main() {
	direction := flag.String("direction", "up", "Migration direction (up/down)")
	flag.Parse()

	// Execute the migration
	migrater, err := chdb.NewMigrator(
		chdb.ClickHouseConfig{
			ConnectionString: os.Getenv("CLICKHOUSE_CONNECTION_STRING"),
			Username:         os.Getenv("CLICKHOUSE_USERNAME"),
			Password:         os.Getenv("CLICKHOUSE_PASSWORD"),
			Database:         os.Getenv("CLICKHOUSE_DATABASE"),
		},
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
		if strings.Contains(err.Error(), "no change") {
			fmt.Println("No change")
			return
		}
		log.Fatalf("Failed to execute migration: %v", err)
	}

	fmt.Println("Migration completed successfully.")
}
