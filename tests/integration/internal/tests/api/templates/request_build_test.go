package api_templates

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestRequestTemplateBuild(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	db := setup.GetTestDBClient(t)
	defaultFreeDisk := readFreeDiskSize(t, db,
		`SELECT default_free_disk_size_mb FROM public.team_limits WHERE id = $1`, setup.TeamID)

	for _, test := range []struct {
		name      string
		requested *api.FreeDiskSpaceMB
		want      int64
	}{
		{name: "default", want: defaultFreeDisk},
		{name: "explicit", requested: new(api.FreeDiskSpaceMB(1024)), want: 1024},
		{name: "zero", requested: new(api.FreeDiskSpaceMB(0)), want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
				Name:            new("test-request-build-" + test.name),
				CpuCount:        new(api.CPUCount(2)),
				MemoryMB:        new(api.MemoryMB(1024)),
				FreeDiskSpaceMB: test.requested,
			}, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, resp.StatusCode())
			require.NotNil(t, resp.JSON202)
			assert.Equal(t, test.want, readFreeDiskSize(t, db,
				`SELECT free_disk_size_mb FROM public.env_builds WHERE id = $1`, resp.JSON202.BuildID))
		})
	}

	t.Run("above team limit", func(t *testing.T) {
		t.Parallel()

		resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
			Name:            new("test-request-build-too-much-free-disk"),
			FreeDiskSpaceMB: new(api.FreeDiskSpaceMB(1 << 30)),
		}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode())
		require.NotNil(t, resp.JSON400)
		assert.True(t, strings.HasPrefix(resp.JSON400.Message, "Free disk space can't be higher than"),
			"unexpected error: %s", resp.JSON400.Message)
	})
}

func readFreeDiskSize(t *testing.T, db *setup.Database, query string, args ...any) int64 {
	t.Helper()

	var value int64
	err := db.Db.TestsRawSQLQuery(t.Context(), query, func(rows pgx.Rows) error {
		rows.Next()

		return rows.Scan(&value)
	}, args...)
	require.NoError(t, err)

	return value
}

func TestRequestTemplateTooLowCPU(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
		Name:     new("test-request-build-too-low-cpu"),
		CpuCount: new(api.CPUCount(0)),
		MemoryMB: new(api.MemoryMB(1024)),
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.NotNil(t, resp.JSON400)
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "validation error"), "error should have prefix 'validation error', the error is '%s'", resp.JSON400.Message)
}

func TestRequestTemplateTooLowRAM(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
		Name:     new("test-request-build-too-low-ram"),
		CpuCount: new(api.CPUCount(2)),
		MemoryMB: new(api.MemoryMB(32)),
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.NotNil(t, resp.JSON400)
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "validation error"), "error should have prefix 'validation error', the error is '%s'", resp.JSON400.Message)
}

func TestRequestTemplateTooHighCPU(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
		Name:     new("test-request-build-too-high-cpu"),
		CpuCount: new(api.CPUCount(1024)),
		MemoryMB: new(api.MemoryMB(1024)),
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.NotNil(t, resp.JSON400)
	assert.Equal(t, "CPU count must be at most 32", resp.JSON400.Message)
}

func TestRequestTemplateOddCPU(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
		Name:     new("test-request-build-odd-cpu"),
		CpuCount: new(api.CPUCount(3)),
		MemoryMB: new(api.MemoryMB(1024)),
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.NotNil(t, resp.JSON400)
	assert.Equal(t, "CPU count must be 1 or an even number", resp.JSON400.Message)
}

func TestRequestTemplateTooHighMemory(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
		Name:     new("test-request-build-too-high-memory"),
		CpuCount: new(api.CPUCount(2)),
		MemoryMB: new(api.MemoryMB(1024 * 1024)),
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.NotNil(t, resp.JSON400)
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "Memory can't be higher than"), "error should have prefix 'Memory can't be higher than', the error is '%s'", resp.JSON400.Message)
}

func TestRequestTemplateMemoryNonDivisibleBy2(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostV3TemplatesWithResponse(t.Context(), api.TemplateBuildRequestV3{
		Name:     new("test-request-build-memory-odd"),
		CpuCount: new(api.CPUCount(2)),
		MemoryMB: new(api.MemoryMB(1001)),
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.NotNil(t, resp.JSON400)
	assert.Equal(t, "Memory must be divisible by 2", resp.JSON400.Message)
}
