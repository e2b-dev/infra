package tests

import (
	"context"
	"log"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestMain(m *testing.M) {
	log.Println("Setting up test environment")

	// Start a sandbox before all tests to cache the necessary files for the base template
	c := setup.GetAPIClient()
	sbxTimeout := int32(60)
	_, err := c.PostSandboxesWithResponse(context.Background(), api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	if err != nil {
		log.Fatal(err)

		return
	}

	log.Println("Running tests")
	m.Run()
	log.Println("Finished running tests, showing report")
}
