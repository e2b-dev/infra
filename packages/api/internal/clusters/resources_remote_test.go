package clusters

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func TestCheckEdgeLogsPidFilteringCompatibility(t *testing.T) {
	t.Parallel()

	pid := "42"
	emptyPid := ""

	responseWithHeader := &http.Response{Header: http.Header{}}
	responseWithHeader.Header.Set(consts.EdgeFeatureSandboxLogsPidFilteringEnabledHeader, "true")

	testCases := []struct {
		name      string
		pid       *string
		response  *http.Response
		expectErr bool
	}{
		{
			name:      "no pid requested",
			pid:       nil,
			response:  &http.Response{Header: http.Header{}},
			expectErr: false,
		},
		{
			name:      "empty pid requested",
			pid:       &emptyPid,
			response:  &http.Response{Header: http.Header{}},
			expectErr: false,
		},
		{
			name:      "pid requested and edge advertises support",
			pid:       &pid,
			response:  responseWithHeader,
			expectErr: false,
		},
		{
			name:      "pid requested but edge does not advertise support",
			pid:       &pid,
			response:  &http.Response{Header: http.Header{}},
			expectErr: true,
		},
		{
			name:      "pid requested with nil response",
			pid:       &pid,
			response:  nil,
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider := &ClusterResourceProviderImpl{clusterID: uuid.New()}

			apiErr := provider.checkEdgeLogsPidFilteringCompatibility(t.Context(), "sandbox-id", tc.pid, tc.response)
			if tc.expectErr {
				require.NotNil(t, apiErr)
				assert.Equal(t, http.StatusNotImplemented, apiErr.Code)
			} else {
				assert.Nil(t, apiErr)
			}
		})
	}
}
