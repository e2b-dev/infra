package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/integration-tests/internal/setup"

	"github.com/stretchr/testify/assert"
)

func TestHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient(t)

	resp, err := c.GetHealthWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode())
	assert.Equal(t, "Health check successful", string(resp.Body))
}
