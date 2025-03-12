package main

import (
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"text/template"

	_ "github.com/lib/pq"
)

type SeedData struct {
	APIKey  string
	EnvId   string
	BuildId string
	TeamId  string
	UserId  string
}

func main() {
	fileName := flag.String("file", "", "file name to seed")
	flag.Parse()

	if fileName == nil || *fileName == "" {
		log.Fatalf("File name is required")
	}

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
	tmpl, err := template.ParseFiles(*fileName)
	if err != nil {
		log.Fatalf("Failed to parse seed file: %v", err)
	}

	var parsed bytes.Buffer
	err = tmpl.Execute(&parsed, SeedData{
		APIKey:  os.Getenv("TESTS_E2B_API_KEY"),
		EnvId:   os.Getenv("TESTS_SANDBOX_TEMPLATE_ID"),
		BuildId: os.Getenv("TESTS_SANDBOX_BUILD_ID"),
		TeamId:  os.Getenv("TESTS_SANDBOX_TEAM_ID"),
		UserId:  os.Getenv("TESTS_SANDBOX_USER_ID"),
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
