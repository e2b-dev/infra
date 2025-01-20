package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

func main() {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	if connectionString == "" {
		log.Fatalf("POSTGRES_CONNECTION_STRING is not set")
	}

	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	migration, err := os.ReadFile("migration.sql")
	if err != nil {
		log.Fatalf("Failed to read migration file: %v", err)
	}

	// Execute the migration
	_, err = db.Exec(string(migration))
	if err != nil {
		log.Fatalf("Failed to execute migration: %v", err)
	}

	fmt.Println("Migration completed successfully.")
}
