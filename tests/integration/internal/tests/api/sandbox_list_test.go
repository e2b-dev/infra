package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"

	"github.com/stretchr/testify/assert"
)

func pauseSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())
}

func TestSandboxList(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox for testing
	sbx := utils.SetupSandboxWithCleanup(t, c)

	// Test basic list functionality
	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.GreaterOrEqual(t, len(*listResponse.JSON200), 1)

	// Verify our sandbox is in the list
	found := false
	for _, s := range *listResponse.JSON200 {
		if s.SandboxID == sbx.SandboxID {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestSandboxListWithFilter(t *testing.T) {
	c := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, c)

	metadataString := "sandboxType=test"

	// List with filter
	listResponse, err := c.GetV2SandboxesWithResponse(context.Background(), &api.GetV2SandboxesParams{
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sbx.SandboxID, (*listResponse.JSON200)[0].SandboxID)
}

func TestSandboxListRunning(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox
	sbx := utils.SetupSandboxWithCleanup(t, c)

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
		if s.SandboxID == sbx.SandboxID {
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
	sbx := utils.SetupSandboxWithCleanup(t, c)
	sandboxID := sbx.SandboxID

	pauseSandbox(t, c, sandboxID)

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
	sbx1 := utils.SetupSandboxWithCleanup(t, c)
	sandbox1ID := sbx1.SandboxID

	sbx2 := utils.SetupSandboxWithCleanup(t, c)
	sandbox2ID := sbx2.SandboxID

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
	sbx1 := utils.SetupSandboxWithCleanup(t, c)
	sandbox1ID := sbx1.SandboxID
	pauseSandbox(t, c, sandbox1ID)

	sbx2 := utils.SetupSandboxWithCleanup(t, c)
	sandbox2ID := sbx2.SandboxID
	pauseSandbox(t, c, sandbox2ID)

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
	sbx1 := utils.SetupSandboxWithCleanup(t, c)
	sandbox1ID := sbx1.SandboxID

	sbx2 := utils.SetupSandboxWithCleanup(t, c)
	sandbox2ID := sbx2.SandboxID

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
func TestSandboxListRunningV1(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox
	sbx := utils.SetupSandboxWithCleanup(t, c)

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
		if s.SandboxID == sbx.SandboxID {
			found = true
			assert.Equal(t, api.Running, s.State)
			break
		}
	}
	assert.True(t, found)
}

func TestSandboxListWithFilterV1(t *testing.T) {
	c := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, c)

	metadataString := "sandboxType=test"

	// List with filter
	listResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
		Metadata: &metadataString,
	}, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 1, len(*listResponse.JSON200))
	assert.Equal(t, sbx.SandboxID, (*listResponse.JSON200)[0].SandboxID)
}

func TestSandboxListSortedV1(t *testing.T) {
	c := setup.GetAPIClient()

	// Create three sandboxes
	sbx1 := utils.SetupSandboxWithCleanup(t, c)
	sbx2 := utils.SetupSandboxWithCleanup(t, c)
	sbx3 := utils.SetupSandboxWithCleanup(t, c)

	// List with filter
	listResponse, err := c.GetSandboxesWithResponse(context.Background(), nil, setup.WithAPIKey())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResponse.StatusCode())
	assert.Equal(t, 3, len(*listResponse.JSON200))
	assert.Equal(t, sbx1.SandboxID, (*listResponse.JSON200)[2].SandboxID)
	assert.Equal(t, sbx2.SandboxID, (*listResponse.JSON200)[1].SandboxID)
	assert.Equal(t, sbx3.SandboxID, (*listResponse.JSON200)[0].SandboxID)
}
