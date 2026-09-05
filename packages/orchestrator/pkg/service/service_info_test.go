//go:build linux

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestServiceStatusOverride(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		from, to orchestratorinfo.ServiceInfoStatus
		wantCode codes.Code
	}{
		{orchestratorinfo.ServiceInfoStatus_Draining, orchestratorinfo.ServiceInfoStatus_Healthy, codes.FailedPrecondition},
		{orchestratorinfo.ServiceInfoStatus_Draining, orchestratorinfo.ServiceInfoStatus_Standby, codes.FailedPrecondition},
		{orchestratorinfo.ServiceInfoStatus_Draining, orchestratorinfo.ServiceInfoStatus_Draining, codes.OK},
		{orchestratorinfo.ServiceInfoStatus_Healthy, orchestratorinfo.ServiceInfoStatus_Draining, codes.OK},
		{orchestratorinfo.ServiceInfoStatus_Standby, orchestratorinfo.ServiceInfoStatus_Healthy, codes.OK},
		{orchestratorinfo.ServiceInfoStatus_Healthy, orchestratorinfo.ServiceInfoStatus_Standby, codes.OK},
	} {
		t.Run(tc.from.String()+"To"+tc.to.String(), func(t *testing.T) {
			t.Parallel()

			info := &ServiceInfo{}
			info.SetStatus(t.Context(), tc.from)
			before := info.GetStatus()
			server := &Server{info: info}

			response, err := server.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
				ServiceStatus: tc.to,
			})

			require.Equal(t, tc.wantCode, status.Code(err))
			if tc.wantCode != codes.OK {
				require.Nil(t, response)
				require.Equal(t, before, info.GetStatus())

				return
			}

			require.NoError(t, err)
			require.NotNil(t, response)
			require.Equal(t, tc.to, info.GetStatus().Status)
		})
	}
}
