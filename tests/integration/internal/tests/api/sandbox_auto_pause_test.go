package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestSandboxAutoPausePauseResume(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbxTimeout := int32(60)
	autoPause := true
	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		AutoPause:  &autoPause,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, sbxCreate.StatusCode())
	require.NotNil(t, sbxCreate.JSON201)

	sbxId := sbxCreate.JSON201.SandboxID
	// Pause the sandbox
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbxId, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	// Resume the sandbox with auto-pause enabled
	_, err = c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	// Set timeout to 0 to force sandbox to be stopped
	_, err = c.PostSandboxesSandboxIDTimeout(ctx, sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == "paused"
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox again to check if it resumes correctly
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)
	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxCreate.JSON201.SandboxID)
}

func TestSandboxAutoPauseResumePersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbxTimeout := int32(60)
	autoPause := true
	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		AutoPause:  &autoPause,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, sbxCreate.StatusCode())
	require.NotNil(t, sbxCreate.JSON201)

	sbxId := sbxCreate.JSON201.SandboxID
	// Set timeout to 0 to force sandbox to be stopped
	_, err = c.PostSandboxesSandboxIDTimeout(ctx, sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == "paused"
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox with auto-pause enabled
	_, err = c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	// Set timeout to 0 to force sandbox to be stopped
	_, err = c.PostSandboxesSandboxIDTimeout(ctx, sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == "paused"
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox again to check if it resumes correctly
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)
	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxCreate.JSON201.SandboxID)
}

func TestSandboxNotAutoPause(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbxTimeout := int32(60)
	autoPause := false
	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		AutoPause:  &autoPause,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, sbxCreate.StatusCode())
	require.NotNil(t, sbxCreate.JSON201)

	sbxId := sbxCreate.JSON201.SandboxID
	// Set timeout to 0 to force sandbox to be stopped
	_, err = c.PostSandboxesSandboxIDTimeout(ctx, sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		return res.StatusCode() == http.StatusNotFound
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox with auto-pause enabled
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, sbxResume.StatusCode())
}
