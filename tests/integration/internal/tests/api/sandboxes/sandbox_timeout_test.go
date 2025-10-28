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

func TestSandboxTimeout(t *testing.T) {
	c := setup.GetAPIClient()
	testCases := []struct {
		name   string
		extend bool

		initialDuration int32
		newDuration     int32
	}{
		{
			name:            "extend",
			extend:          true,
			initialDuration: 60,
			newDuration:     120,
		},
		{
			name:            "shorten",
			extend:          false,
			initialDuration: 120,
			newDuration:     60,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(tc.initialDuration))

			// Get initial sandbox details
			detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, detailResp.StatusCode())
			require.NotNil(t, detailResp.JSON200)

			initialEndTime := detailResp.JSON200.EndAt

			timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbx.SandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
				Timeout: tc.newDuration,
			}, setup.WithAPIKey())
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, timeoutResp.StatusCode())

			detailResp2, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, detailResp2.StatusCode())
			require.NotNil(t, detailResp2.JSON200)

			newEndTime := detailResp2.JSON200.EndAt

			assert.Equal(t, tc.extend, newEndTime.After(initialEndTime), "End time should be extended")
			assert.NotEqual(t, initialEndTime, newEndTime, "End time should be updated")
		})
	}
}

func TestSandboxTimeout_NotFound(t *testing.T) {
	c := setup.GetAPIClient()

	timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), "nonexistent-sandbox-id", api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 60,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, timeoutResp.StatusCode())
}
