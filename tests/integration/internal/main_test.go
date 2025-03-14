package internal

import (
	"context"
	"github.com/stretchr/testify/assert"
	"log"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestMain(m *testing.M) {
	log.Println("Setting up test environment")
	m.Run()
	log.Println("Environment set up")
}

// TestCacheTemplate starts a sandbox before all tests to cache the necessary files for the base template.
func TestCacheTemplate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbxTimeout := int32(60)
	_, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())

	assert.NoError(t, err)
}
