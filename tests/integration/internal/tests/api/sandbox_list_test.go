package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

// setupSandbox creates a new sandbox and returns its ID
func setupSandbox(t *testing.T, c *api.ClientWithResponses) string {
	createSandboxResponse, err := c.PostSandboxesWithResponse(context.Background(), api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Metadata: &api.SandboxMetadata{
			"sandboxType": "test",
		},
	}, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

	return createSandboxResponse.JSON201.SandboxID
}

// teardownSandbox kills the sandbox with the given ID
func teardownSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())
}

func pauseSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())
}

func TestSandboxList(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox for testing
	sandboxID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandboxID)

	// Test basic list functionality
	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.GreaterOrEqual(t, len(*listResponse.JSON200), 1)

	// Verify our sandbox is in the list
	found := false
	for _, s := range *listResponse.JSON200 {
		if s.SandboxID == sandboxID {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestSandboxListWithFilter(t *testing.T) {
	c := setup.GetAPIClient()

	sandboxID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandboxID)

	metadataString := "sandboxType=test"

	// List with filter
	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sandboxID, (*listResponse.JSON200)[0].SandboxID)
}

func TestSandboxListRunning(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox
	sandboxID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandboxID)

	metadataString := "sandboxType=test"

	// List running sandboxes
	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.GreaterOrEqual(t, len(*listResponse.JSON200), 1)

	// Verify our running sandbox is in the list
	found := false
	for _, s := range *listResponse.JSON200 {
		if s.SandboxID == sandboxID {
			found = true
			assert.Equal(t, api.Running, s.State)
			break
		}
	}
	assert.True(t, found)
}

func TestSandboxListPaused(t *testing.T) {
	c := setup.GetAPIClient()

	// Create and pause a sandbox
	sandboxID := setupSandbox(t, c)
	pauseSandbox(t, c, sandboxID)

	defer teardownSandbox(t, c, sandboxID)

	metadataString := "sandboxType=test"

	// List paused sandboxes
	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		State:    &[]api.SandboxState{api.Paused},
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.GreaterOrEqual(t, len(*listResponse.JSON200), 1)

	// Verify our paused sandbox is in the list
	found := false
	for _, s := range *listResponse.JSON200 {
		if s.SandboxID == sandboxID {
			found = true
			assert.Equal(t, api.Paused, s.State)
			break
		}
	}
	assert.True(t, found)
}

func TestSandboxListPaginationRunning(t *testing.T) {
	c := setup.GetAPIClient()

	// Create two sandboxes
	sandbox1ID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandbox1ID)

	sandbox2ID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandbox2ID)

	// Test pagination with limit
	var limit int32 = 1
	metadataString := "sandboxType=test"

	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Limit:    &limit,
		State:    &[]api.SandboxState{api.Running},
		Metadata: &metadataString,
	}, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sandbox2ID, (*listResponse.JSON200)[0].SandboxID)

	// Get second page using the next token from first response
	nextToken := listResponse.HTTPResponse.Header.Get("X-Next-Token")
	assert.NotEmpty(t, nextToken)

	secondPageResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Limit:     &limit,
		NextToken: &nextToken,
		State:     &[]api.SandboxState{api.Running},
		Metadata:  &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, secondPageResponse.StatusCode())
	assert.Equal(t, 1, len(*secondPageResponse.JSON200))
	assert.Equal(t, sandbox1ID, (*secondPageResponse.JSON200)[0].SandboxID)

	// No more pages
	nextToken = secondPageResponse.HTTPResponse.Header.Get("X-Next-Token")
	assert.Empty(t, nextToken)
}

func TestSandboxListPaginationPaused(t *testing.T) {
	c := setup.GetAPIClient()

	// Create two paused sandboxes
	sandbox1ID := setupSandbox(t, c)
	pauseSandbox(t, c, sandbox1ID)

	defer teardownSandbox(t, c, sandbox1ID)

	sandbox2ID := setupSandbox(t, c)
	pauseSandbox(t, c, sandbox2ID)

	defer teardownSandbox(t, c, sandbox2ID)

	// Test pagination with limit
	var limit int32 = 1
	metadataString := "sandboxType=test"

	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Limit:    &limit,
		State:    &[]api.SandboxState{api.Paused},
		Metadata: &metadataString,
	}, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sandbox2ID, (*listResponse.JSON200)[0].SandboxID)

	// Get second page using the next token from first response
	nextToken := listResponse.HTTPResponse.Header.Get("X-Next-Token")
	assert.NotEmpty(t, nextToken)

	secondPageResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Limit:     &limit,
		NextToken: &nextToken,
		State:     &[]api.SandboxState{api.Paused},
		Metadata:  &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, secondPageResponse.StatusCode())
	assert.Equal(t, 1, len(*secondPageResponse.JSON200))
	assert.Equal(t, sandbox1ID, (*secondPageResponse.JSON200)[0].SandboxID)

	// No more pages
	nextToken = secondPageResponse.HTTPResponse.Header.Get("X-Next-Token")
	assert.Empty(t, nextToken)
}

func TestSandboxListPaginationRunningAndPaused(t *testing.T) {
	c := setup.GetAPIClient()

	// Create two sandboxes
	sandbox1ID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandbox1ID)

	sandbox2ID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandbox2ID)

	// Pause the second sandbox
	pauseSandbox(t, c, sandbox2ID)

	// Test pagination with limit
	var limit int32 = 1
	metadataString := "sandboxType=test"

	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Limit:    &limit,
		State:    &[]api.SandboxState{api.Running, api.Paused},
		Metadata: &metadataString,
	}, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sandbox2ID, (*listResponse.JSON200)[0].SandboxID)

	// Get second page using the next token from first response
	nextToken := listResponse.HTTPResponse.Header.Get("X-Next-Token")
	assert.NotEmpty(t, nextToken)

	secondPageResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Limit:     &limit,
		NextToken: &nextToken,
		State:     &[]api.SandboxState{api.Running, api.Paused},
		Metadata:  &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, secondPageResponse.StatusCode())
	assert.Equal(t, 1, len(*secondPageResponse.JSON200))
	assert.Equal(t, sandbox1ID, (*secondPageResponse.JSON200)[0].SandboxID)

	// No more pages
	nextToken = secondPageResponse.HTTPResponse.Header.Get("X-Next-Token")
	assert.Empty(t, nextToken)
}

// legacy tests
func TestSandboxListRunningLegacy(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox
	sandboxID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandboxID)

	metadataString := "sandboxType=test"

	// List running sandboxes
	listResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.GreaterOrEqual(t, len(*listResponse.JSON200), 1)

	// Verify our running sandbox is in the list
	found := false
	for _, s := range *listResponse.JSON200 {
		if s.SandboxID == sandboxID {
			found = true
			assert.Equal(t, api.Running, s.State)
			break
		}
	}
	assert.True(t, found)
}

func TestSandboxListWithFilterLegacy(t *testing.T) {
	c := setup.GetAPIClient()

	sandboxID := setupSandbox(t, c)
	defer teardownSandbox(t, c, sandboxID)

	metadataString := "sandboxType=test"

	// List with filter
	listResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sandboxID, (*listResponse.JSON200)[0].SandboxID)
}
