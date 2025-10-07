package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestCreateSecret(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	t.Run("succeeds with valid allowlist", func(t *testing.T) {
		// Create the secret
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret",
			Value:       "secret-value-123",
			Description: "Test secret description",
			Allowlist:   []string{"*.example.com", "api.test.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, "test-secret", resp.JSON201.Label)
		assert.Equal(t, "Test secret description", resp.JSON201.Description)
		assert.NotEmpty(t, resp.JSON201.Id)
		assert.Equal(t, []string{"*.example.com", "api.test.com"}, resp.JSON201.Allowlist)
	})

	t.Run("succeeds with empty allowlist defaults to wildcard", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-empty-allowlist",
			Value:       "secret-value-456",
			Description: "Test with empty allowlist",
			Allowlist:   []string{},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, []string{"*"}, resp.JSON201.Allowlist)
	})

	t.Run("succeeds with wildcard patterns", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-wildcards",
			Value:       "secret-value-789",
			Description: "Test with various wildcard patterns",
			Allowlist:   []string{"*", "*.domain.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, []string{"*", "*.domain.com"}, resp.JSON201.Allowlist)
	})

	t.Run("fails with glob question mark pattern", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-glob-question",
			Value:       "secret-value",
			Description: "Test with glob question mark",
			Allowlist:   []string{"api-?.test.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with glob bracket pattern", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-glob-bracket",
			Value:       "secret-value",
			Description: "Test with glob bracket pattern",
			Allowlist:   []string{"service[1-3].example.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with too many hosts in allowlist", func(t *testing.T) {
		tooManyHosts := make([]string, 11)
		for i := range 11 {
			tooManyHosts[i] = fmt.Sprintf("host%d.example.com", i)
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-too-many-hosts",
			Value:       "secret-value",
			Description: "Test with too many hosts",
			Allowlist:   tooManyHosts,
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "Too many hosts in allowlist")
	})

	t.Run("fails with invalid host pattern", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-invalid-pattern",
			Value:       "secret-value",
			Description: "Test with invalid pattern",
			Allowlist:   []string{"[invalid-pattern"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with invalid bracket pattern", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-invalid-brackets",
			Value:       "secret-value",
			Description: "Test with unclosed brackets",
			Allowlist:   []string{"host[1-3.example.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with backslash in pattern", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-backslash",
			Value:       "secret-value",
			Description: "Test with backslash",
			Allowlist:   []string{"host\\example.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with URL scheme https", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-https-scheme",
			Value:       "secret-value",
			Description: "Test with https scheme",
			Allowlist:   []string{"https://example.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "cannot contain schemes")
	})

	t.Run("fails with URL scheme http", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-http-scheme",
			Value:       "secret-value",
			Description: "Test with http scheme",
			Allowlist:   []string{"http://example.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "cannot contain schemes")
	})

	t.Run("fails with URL path", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-url-path",
			Value:       "secret-value",
			Description: "Test with URL path",
			Allowlist:   []string{"example.com/api"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "cannot contain schemes")
	})

	t.Run("fails with port number", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-port",
			Value:       "secret-value",
			Description: "Test with port number",
			Allowlist:   []string{"example.com:8080"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with invalid characters", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-invalid-chars",
			Value:       "secret-value",
			Description: "Test with invalid characters",
			Allowlist:   []string{"example!.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with spaces", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-spaces",
			Value:       "secret-value",
			Description: "Test with spaces",
			Allowlist:   []string{"example .com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("succeeds with valid hostnames", func(t *testing.T) {
		validHostnames := []string{
			"example.com",
			"sub.example.com",
			"sub-domain.example.com",
			"example123.com",
			"123-example.com",
			"a.b.c.d.example.com",
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-valid-hostnames",
			Value:       "secret-value",
			Description: "Test with valid hostnames",
			Allowlist:   validHostnames,
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, validHostnames, resp.JSON201.Allowlist)
	})

	t.Run("succeeds with valid wildcard patterns", func(t *testing.T) {
		validWildcards := []string{
			"*",
			"*.example.com",
			"*.*.example.com",
			"api.*.example.com",
			"*.*",
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-valid-wildcards",
			Value:       "secret-value",
			Description: "Test with valid wildcard patterns",
			Allowlist:   validWildcards,
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, validWildcards, resp.JSON201.Allowlist)
	})

	t.Run("fails with hostname starting with hyphen", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-hyphen-start",
			Value:       "secret-value",
			Description: "Test with hostname starting with hyphen",
			Allowlist:   []string{"-example.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with hostname ending with hyphen", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-secret-hyphen-end",
			Value:       "secret-value",
			Description: "Test with hostname ending with hyphen",
			Allowlist:   []string{"example-.com"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "invalid hostname pattern")
	})

	t.Run("fails with empty secret value", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-empty-value",
			Value:       "",
			Description: "Test with empty secret value",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "Secret value cannot be empty")
	})

	t.Run("fails with secret value exceeding 8KB", func(t *testing.T) {
		// Create a value larger than 8KB (8192 bytes)
		largeValue := ""
		for range 8193 {
			largeValue += "a"
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-large-value",
			Value:       largeValue,
			Description: "Test with large secret value",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		assert.Contains(t, string(resp.Body), "Secret value cannot exceed")
	})

	t.Run("succeeds with secret value exactly 8KB", func(t *testing.T) {
		// Create a value exactly 8KB (8192 bytes)
		maxValue := ""
		for range 8192 {
			maxValue += "a"
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-max-value",
			Value:       maxValue,
			Description: "Test with max secret value",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.NotEmpty(t, resp.JSON201.Id)
	})

	t.Run("fails with empty label", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "",
			Value:       "secret-value",
			Description: "Test with empty label",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	t.Run("fails with label exceeding 256 characters", func(t *testing.T) {
		longLabel := ""
		for range 257 {
			longLabel += "a"
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       longLabel,
			Value:       "secret-value",
			Description: "Test with long label",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	t.Run("succeeds with label exactly 256 characters", func(t *testing.T) {
		maxLabel := ""
		for i := 0; i < 256; i++ {
			maxLabel += "a"
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       maxLabel,
			Value:       "secret-value",
			Description: "Test with max label",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, maxLabel, resp.JSON201.Label)
	})

	t.Run("fails with description exceeding 1024 characters", func(t *testing.T) {
		longDescription := ""
		for range 1025 {
			longDescription += "a"
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-long-description",
			Value:       "secret-value",
			Description: longDescription,
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	t.Run("succeeds with description exactly 1024 characters", func(t *testing.T) {
		maxDescription := ""
		for range 1024 {
			maxDescription += "a"
		}

		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-max-description",
			Value:       "secret-value",
			Description: maxDescription,
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, maxDescription, resp.JSON201.Description)
	})

	t.Run("fails without API key", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-no-auth",
			Value:       "secret-value",
			Description: "Test without authentication",
			Allowlist:   []string{"*"},
		})
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})

	t.Run("fails with invalid API key", func(t *testing.T) {
		resp, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-invalid-auth",
			Value:       "secret-value",
			Description: "Test with invalid authentication",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey("invalid-api-key-789"))
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})
}

func TestUpdateSecret(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	t.Run("succeeds with valid data", func(t *testing.T) {
		// Create a secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-update-original",
			Value:       "secret-value-original",
			Description: "Original description",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		// Update the secret
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "test-update-new",
			Description: "Updated description",
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respU.StatusCode())

		// Verify the changes by listing secrets
		respL, err := c.GetSecretsWithResponse(ctx, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respL.StatusCode())

		// Find the updated secret
		var found bool
		for _, secret := range *respL.JSON200 {
			if secret.Id == respC.JSON201.Id {
				found = true
				assert.Equal(t, "test-update-new", secret.Label)
				assert.NotNil(t, secret.Description)
				assert.Equal(t, "Updated description", *secret.Description)
				break
			}
		}
		assert.True(t, found, "Updated secret should be in the list")
	})

	t.Run("fails with empty label", func(t *testing.T) {
		// Create a secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-update-empty-label",
			Value:       "secret-value",
			Description: "Test secret",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		// Try to update with empty label
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "",
			Description: "Description",
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusBadRequest, respU.StatusCode())
		assert.Contains(t, string(respU.Body), "Label cannot be empty")
	})

	t.Run("fails with label exceeding 256 characters", func(t *testing.T) {
		// Create a secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-update-long-label",
			Value:       "secret-value",
			Description: "Test secret",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		longLabel := ""
		for i := 0; i < 257; i++ {
			longLabel += "a"
		}

		// Try to update with long label
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       longLabel,
			Description: "Description",
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusBadRequest, respU.StatusCode())
		assert.Contains(t, string(respU.Body), "Label cannot exceed")
	})

	t.Run("succeeds with label exactly 256 characters", func(t *testing.T) {
		// Create a secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-update-max-label",
			Value:       "secret-value",
			Description: "Test secret",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		maxLabel := ""
		for range 256 {
			maxLabel += "a"
		}

		// Update with max label
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       maxLabel,
			Description: "Description",
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respU.StatusCode())
	})

	t.Run("fails with description exceeding 1024 characters", func(t *testing.T) {
		// Create a secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-update-long-desc",
			Value:       "secret-value",
			Description: "Test secret",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		longDescription := ""
		for range 1025 {
			longDescription += "a"
		}

		// Try to update with long description
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "Label",
			Description: longDescription,
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusBadRequest, respU.StatusCode())
		assert.Contains(t, string(respU.Body), "Description cannot exceed")
	})

	t.Run("succeeds with description exactly 1024 characters", func(t *testing.T) {
		// Create a secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-update-max-desc",
			Value:       "secret-value",
			Description: "Test secret",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		maxDescription := ""
		for range 1024 {
			maxDescription += "a"
		}

		// Update with max description
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "Label",
			Description: maxDescription,
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respU.StatusCode())
	})

	t.Run("fails when secret does not exist", func(t *testing.T) {
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, uuid.New().String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "New label",
			Description: "New description",
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respU.StatusCode())
		assert.Contains(t, string(respU.Body), "Secret not found")
	})

	t.Run("cannot update secret from another team", func(t *testing.T) {
		db := setup.GetTestDBClient(t)

		// Create a second team with its own API key
		team2ID := utils.CreateTeam(t, c, db, "test-team-secrets-update-foreign")
		utils.AddUserToTeam(t, c, db, team2ID, setup.UserID)
		team2APIKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, team2ID)

		// Create a secret on team2
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "team2-secret-update",
			Value:       "secret-from-team2-update",
			Description: "Secret belonging to team2",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey(team2APIKey))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		// Try to update team2's secret using the default team's API key
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "Updated label",
			Description: "Updated description",
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respU.StatusCode(), "Should not be able to update another team's secret")
	})

	t.Run("fails without API key", func(t *testing.T) {
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, uuid.New().String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "New label",
			Description: "New description",
		})
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusUnauthorized, respU.StatusCode())
	})

	t.Run("fails with invalid API key", func(t *testing.T) {
		respU, err := c.PatchSecretsSecretIDWithResponse(ctx, uuid.New().String(), api.PatchSecretsSecretIDJSONRequestBody{
			Label:       "New label",
			Description: "New description",
		}, setup.WithAPIKey("invalid-api-key-123"))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusUnauthorized, respU.StatusCode())
	})
}

func TestDeleteSecret(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	t.Run("succeeds", func(t *testing.T) {
		// Create the secret
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-delete",
			Value:       "secret-to-delete",
			Description: "Will be deleted",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		// Delete the secret
		respD, err := c.DeleteSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNoContent, respD.StatusCode())
	})

	t.Run("id does not exist", func(t *testing.T) {
		respD, err := c.DeleteSecretsSecretIDWithResponse(ctx, uuid.New().String(), setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respD.StatusCode())
	})

	t.Run("cannot delete secret from another team", func(t *testing.T) {
		db := setup.GetTestDBClient(t)

		// Create a second team with its own API key
		team2ID := utils.CreateTeam(t, c, db, "test-team-secrets-foreign")
		utils.AddUserToTeam(t, c, db, team2ID, setup.UserID)
		team2APIKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, team2ID)

		// Create a secret on team2
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "team2-secret",
			Value:       "secret-from-team2",
			Description: "Secret belonging to team2",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey(team2APIKey))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		// Try to delete team2's secret using the default team's API key
		respD, err := c.DeleteSecretsSecretIDWithResponse(ctx, respC.JSON201.Id.String(), setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respD.StatusCode(), "Should not be able to delete another team's secret")
	})

	t.Run("fails without API key", func(t *testing.T) {
		respD, err := c.DeleteSecretsSecretIDWithResponse(ctx, uuid.New().String())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusUnauthorized, respD.StatusCode())
	})

	t.Run("fails with invalid API key", func(t *testing.T) {
		respD, err := c.DeleteSecretsSecretIDWithResponse(ctx, uuid.New().String(), setup.WithAPIKey("invalid-api-key-123"))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusUnauthorized, respD.StatusCode())
	})
}

func TestListSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	t.Run("succeeds", func(t *testing.T) {
		resp, err := c.GetSecretsWithResponse(ctx, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, resp.StatusCode())
		assert.NotNil(t, resp.JSON200)
	})

	t.Run("returns secrets in descending order by creation time", func(t *testing.T) {
		// Create three secrets in sequence
		secret1, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-order-first",
			Value:       "value1",
			Description: "Created first",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, secret1.StatusCode())

		secret2, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-order-second",
			Value:       "value2",
			Description: "Created second",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, secret2.StatusCode())

		secret3, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "test-order-third",
			Value:       "value3",
			Description: "Created third",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, secret3.StatusCode())

		// List all secrets
		respL, err := c.GetSecretsWithResponse(ctx, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respL.StatusCode())
		assert.NotNil(t, respL.JSON200)

		// Find our three test secrets in the response
		var foundSecrets []api.Secret
		for _, secret := range *respL.JSON200 {
			if secret.Id == secret1.JSON201.Id || secret.Id == secret2.JSON201.Id || secret.Id == secret3.JSON201.Id {
				foundSecrets = append(foundSecrets, secret)
			}
		}

		// Should have found all three
		assert.Len(t, foundSecrets, 3, "Should find all three created secrets")

		// Verify they are in descending order by creation time (newest first)
		assert.True(t, foundSecrets[0].CreatedAt.After(foundSecrets[1].CreatedAt) || foundSecrets[0].CreatedAt.Equal(foundSecrets[1].CreatedAt),
			"First secret should be created after or at same time as second")
		assert.True(t, foundSecrets[1].CreatedAt.After(foundSecrets[2].CreatedAt) || foundSecrets[1].CreatedAt.Equal(foundSecrets[2].CreatedAt),
			"Second secret should be created after or at same time as third")

		// Specifically check that the newest one (secret3) is first
		assert.Equal(t, secret3.JSON201.Id, foundSecrets[0].Id, "Newest secret (third created) should be first in list")
		assert.Equal(t, secret2.JSON201.Id, foundSecrets[1].Id, "Second newest secret should be second in list")
		assert.Equal(t, secret1.JSON201.Id, foundSecrets[2].Id, "Oldest secret (first created) should be last in list")
	})

	t.Run("cannot see secrets from another team", func(t *testing.T) {
		db := setup.GetTestDBClient(t)

		// Create a second team with its own API key
		team2ID := utils.CreateTeam(t, c, db, "test-team-secrets-list-foreign")
		utils.AddUserToTeam(t, c, db, team2ID, setup.UserID)
		team2APIKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, team2ID)

		// Create a secret on team2
		respC, err := c.PostSecretsWithResponse(ctx, api.PostSecretsJSONRequestBody{
			Label:       "team2-secret-list",
			Value:       "secret-from-team2-list",
			Description: "Secret belonging to team2 for list test",
			Allowlist:   []string{"*"},
		}, setup.WithAPIKey(team2APIKey))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		team2SecretID := respC.JSON201.Id

		// List secrets using the default team's API key
		respL, err := c.GetSecretsWithResponse(ctx, setup.WithAPIKey())
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respL.StatusCode())

		// Verify team2's secret is NOT in the list
		for _, secret := range *respL.JSON200 {
			assert.NotEqual(t, team2SecretID, secret.Id, "Should not see secrets from another team")
		}
	})

	t.Run("fails without API key", func(t *testing.T) {
		resp, err := c.GetSecretsWithResponse(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})

	t.Run("fails with invalid API key", func(t *testing.T) {
		resp, err := c.GetSecretsWithResponse(ctx, setup.WithAPIKey("invalid-api-key-456"))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})
}
