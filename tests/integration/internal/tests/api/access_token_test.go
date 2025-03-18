package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
)

func TestCreateAccessToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	resp, err := c.PostAccesstokensWithResponse(ctx, api.PostAccesstokensJSONRequestBody{
		Name: "test",
	}, setup.WithSupabaseToken(t))
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	assert.Equal(t, "test", resp.JSON201.Name)
	assert.NotEmpty(t, resp.JSON201.Token)
	assert.Regexp(t, fmt.Sprintf("^%s.+$", keys.AccessTokenPrefix), resp.JSON201.Token)
}

func TestDeleteAccessToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	t.Run("succeeds", func(t *testing.T) {
		respC, err := c.PostAccesstokensWithResponse(ctx, api.PostAccesstokensJSONRequestBody{
			Name: "test",
		}, setup.WithSupabaseToken(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		respD, err := c.DeleteAccesstokensAccessTokenIDWithResponse(ctx, respC.JSON201.Id.String(), setup.WithSupabaseToken(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNoContent, respD.StatusCode())
	})

	t.Run("id does not exist", func(t *testing.T) {
		respD, err := c.DeleteAccesstokensAccessTokenIDWithResponse(ctx, uuid.New().String(), setup.WithSupabaseToken(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respD.StatusCode())
	})
}
