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

	seed, err := os.ReadFile("seed.sql")
	if err != nil {
		log.Fatalf("Failed to read seed file: %v", err)
	}

	// Execute the seed
	_, err = db.Exec(string(seed))
	if err != nil {
		log.Fatalf("Failed to execute seed: %v", err)
	}

	fmt.Println("Seed completed successfully.")
}
