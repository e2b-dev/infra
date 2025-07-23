package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	resp, err := c.GetHealthWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode())
	assert.Equal(t, "Health check successful", string(resp.Body))
}
