package api_templates

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func int32Pointer(i int32) *int32 {
	return &i
}

func TestRequestTemplateBuild(t *testing.T) {
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: int32Pointer(2),
		MemoryMB: int32Pointer(1024),
	}, setup.WithAccessToken())
	assert.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode())
}

func TestRequestTemplateTooLowCPU(t *testing.T) {
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: int32Pointer(0),
		MemoryMB: int32Pointer(1024),
	}, setup.WithAccessToken())
	assert.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "validation error"), fmt.Sprintf("error should have prefix 'validation error', the error is '%s'", resp.JSON400.Message))
}

func TestRequestTemplateTooLowRAM(t *testing.T) {
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: int32Pointer(2),
		MemoryMB: int32Pointer(32),
	}, setup.WithAccessToken())
	assert.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "validation error"), fmt.Sprintf("error should have prefix 'validation error', the error is '%s'", resp.JSON400.Message))
}

func TestRequestTemplateTooHighCPU(t *testing.T) {
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: int32Pointer(1024),
		MemoryMB: int32Pointer(1024),
	}, setup.WithAccessToken())
	assert.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "CPU count can't be higher than"), fmt.Sprintf("error should have prefix 'CPU count can't be higher than', the error is '%s'", resp.JSON400.Message))
}

func TestRequestTemplateTooHighMemory(t *testing.T) {
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: int32Pointer(2),
		MemoryMB: int32Pointer(1024 * 1024),
	}, setup.WithAccessToken())
	assert.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "Memory can't be higher than"), fmt.Sprintf("error should have prefix 'Memory can't be higher than', the error is '%s'", resp.JSON400.Message))
}

func TestRequestTemplateMemoryNonDivisibleBy2(t *testing.T) {
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: int32Pointer(2),
		MemoryMB: int32Pointer(1001),
	}, setup.WithAccessToken())
	assert.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.Equal(t, "Memory must be divisible by 2", resp.JSON400.Message)
}
