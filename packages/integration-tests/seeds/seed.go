package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"os"
	"text/template"

	_ "github.com/lib/pq"
)

type SeedData struct {
	EnvId   string
	BuildId string
}

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

	// Execute the seed
	tmpl, err := template.ParseFiles("seed.sql")
	if err != nil {
		log.Fatalf("Failed to parse seed file: %v", err)
	}

	var parsed bytes.Buffer
	err = tmpl.Execute(&parsed, SeedData{
		EnvId:   os.Getenv("TESTS_SANDBOX_TEMPLATE_ID"),
		BuildId: os.Getenv("TESTS_SANDBOX_BUILD_ID"),
	})
	if err != nil {
		log.Fatalf("Failed to execute seed: %v", err)
	}

	_, err = db.Exec(parsed.String())
	if err != nil {
		log.Fatalf("Failed to execute seed: %v", err)
	}

	fmt.Println("Seed completed successfully.")
}
