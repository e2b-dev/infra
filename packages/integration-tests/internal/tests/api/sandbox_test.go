package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/integration-tests/internal/api"
	"github.com/e2b-dev/infra/packages/integration-tests/internal/setup"

	"github.com/stretchr/testify/assert"
)

func TestSandboxCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient(t)

	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
}

func TestSandboxCreate2(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient(t)

	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
}
