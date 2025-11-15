package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxRefresh(t *testing.T) {
	c := setup.GetAPIClient()
	testCases := []struct {
		name   string
		extend bool
		same   bool

		initialDuration int
		newDuration     int
	}{
		{
			name:   "extend",
			extend: true,
			same:   false,

			initialDuration: 60,
			newDuration:     120,
		},
		{
			name:            "shorten",
			extend:          false,
			same:            true,
			initialDuration: 120,
			newDuration:     60,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(int32(tc.initialDuration)))

			// Get initial sandbox details
			detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, detailResp.StatusCode())
			require.NotNil(t, detailResp.JSON200)

			initialEndTime := detailResp.JSON200.EndAt

			timeoutResp, err := c.PostSandboxesSandboxIDRefreshesWithResponse(t.Context(), sbx.SandboxID, api.PostSandboxesSandboxIDRefreshesJSONRequestBody{
				Duration: &tc.newDuration,
			}, setup.WithAPIKey())
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, timeoutResp.StatusCode())

			detailResp2, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, detailResp2.StatusCode())
			require.NotNil(t, detailResp2.JSON200)

			newEndTime := detailResp2.JSON200.EndAt

			assert.Equal(t, tc.extend, newEndTime.After(initialEndTime), "End time should be extended")
			assert.Equal(t, tc.same, newEndTime.Equal(initialEndTime), "End time should be updated")
		})
	}
}

func TestSandboxRefresh_NotFound(t *testing.T) {
	c := setup.GetAPIClient()

	timeoutResp, err := c.PostSandboxesSandboxIDRefreshesWithResponse(t.Context(), "nonexistent-sandbox-id", api.PostSandboxesSandboxIDRefreshesJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, timeoutResp.StatusCode())
}
