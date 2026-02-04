package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestCreateAccessToken(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	resp, err := c.PostAccessTokensWithResponse(ctx, api.PostAccessTokensJSONRequestBody{
		Name: "test",
	}, setup.WithSupabaseToken(t))
	if err != nil {
		t.Fatal(err)
	}

	require.Equal(t, http.StatusCreated, resp.StatusCode(), string(resp.Body))
	assert.Equal(t, "test", resp.JSON201.Name)
	assert.NotEmpty(t, resp.JSON201.Token)
	assert.Regexp(t, fmt.Sprintf("^%s.+$", keys.AccessTokenPrefix), resp.JSON201.Token)
}

func TestDeleteAccessToken(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	c := setup.GetAPIClient()

	t.Run("succeeds", func(t *testing.T) {
		t.Parallel()
		respC, err := c.PostAccessTokensWithResponse(ctx, api.PostAccessTokensJSONRequestBody{
			Name: "test",
		}, setup.WithSupabaseToken(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		respD, err := c.DeleteAccessTokensAccessTokenIDWithResponse(ctx, respC.JSON201.Id.String(), setup.WithSupabaseToken(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNoContent, respD.StatusCode())
	})

	t.Run("id does not exist", func(t *testing.T) {
		t.Parallel()
		respD, err := c.DeleteAccessTokensAccessTokenIDWithResponse(ctx, uuid.New().String(), setup.WithSupabaseToken(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respD.StatusCode())
	})
}
