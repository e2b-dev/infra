package sandboxes

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxTimeoutExtendOnly_Extend(t *testing.T) {
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	// Get initial sandbox details
	detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp.StatusCode())
	require.NotNil(t, detailResp.JSON200)

	initialEndTime := detailResp.JSON200.EndAt

	// Extend the timeout with extendOnly=true and a longer timeout
	extendOnly := true
	newTimeout := int32(120)
	timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbx.SandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		ExtendOnly: &extendOnly,
		Timeout:    newTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, timeoutResp.StatusCode())

	// Verify the timeout was extended
	detailResp2, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp2.StatusCode())
	require.NotNil(t, detailResp2.JSON200)

	newEndTime := detailResp2.JSON200.EndAt

	// The new end time should be after the initial end time
	assert.True(t, newEndTime.After(initialEndTime), "End time should be extended")
}

func TestSandboxTimeoutExtendOnly_NoShortenWhenExtendOnlyTrue(t *testing.T) {
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(60))

	// Get initial sandbox details
	detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp.StatusCode())
	require.NotNil(t, detailResp.JSON200)

	initialEndTime := detailResp.JSON200.EndAt

	// Try to set a shorter timeout with extendOnly=true
	extendOnly := true
	shorterTimeout := int32(20)
	timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbx.SandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		ExtendOnly: &extendOnly,
		Timeout:    shorterTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	// Should succeed but not actually shorten the timeout
	assert.Equal(t, http.StatusNoContent, timeoutResp.StatusCode())

	// Verify the timeout was NOT shortened
	detailResp2, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp2.StatusCode())
	require.NotNil(t, detailResp2.JSON200)

	newEndTime := detailResp2.JSON200.EndAt

	// The end time should remain the same (or very close, accounting for time precision)
	timeDiff := newEndTime.Sub(initialEndTime)
	assert.True(t, timeDiff.Abs() < 2*time.Second, "End time should not be shortened when extendOnly is true")
}

func TestSandboxTimeoutExtendOnly_ShortenWhenExtendOnlyFalse(t *testing.T) {
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(60))

	// Get initial sandbox details
	detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp.StatusCode())
	require.NotNil(t, detailResp.JSON200)

	initialEndTime := detailResp.JSON200.EndAt

	// Set a shorter timeout with extendOnly=false (or omitted, defaults to false)
	extendOnly := false
	shorterTimeout := int32(20)
	timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbx.SandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		ExtendOnly: &extendOnly,
		Timeout:    shorterTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, timeoutResp.StatusCode())

	// Verify the timeout was shortened
	detailResp2, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp2.StatusCode())
	require.NotNil(t, detailResp2.JSON200)

	newEndTime := detailResp2.JSON200.EndAt

	// The new end time should be before the initial end time
	assert.True(t, newEndTime.Before(initialEndTime), "End time should be shortened when extendOnly is false")
}

func TestSandboxTimeoutExtendOnly_OmittedDefaultsToFalse(t *testing.T) {
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(60))

	// Get initial sandbox details
	detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp.StatusCode())
	require.NotNil(t, detailResp.JSON200)

	initialEndTime := detailResp.JSON200.EndAt

	// Set a shorter timeout without specifying extendOnly (should default to false)
	shorterTimeout := int32(20)
	timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbx.SandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: shorterTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, timeoutResp.StatusCode())

	// Verify the timeout was shortened
	detailResp2, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detailResp2.StatusCode())
	require.NotNil(t, detailResp2.JSON200)

	newEndTime := detailResp2.JSON200.EndAt

	// The new end time should be before the initial end time
	assert.True(t, newEndTime.Before(initialEndTime), "End time should be shortened when extendOnly is omitted (defaults to false)")
}

func TestSandboxTimeout_NotFound(t *testing.T) {
	c := setup.GetAPIClient()

	// Try to set timeout on non-existent sandbox
	timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), "nonexistent-sandbox-id", api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 60,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, timeoutResp.StatusCode())
}
