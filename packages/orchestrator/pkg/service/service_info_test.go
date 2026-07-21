//go:build linux

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestServiceStatusOverrideRejectsDrainingToStandby(t *testing.T) {
	t.Parallel()

	info := &ServiceInfo{}
	info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Draining)
	server := &Server{info: info}

	_, err := server.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Standby,
	})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, info.GetStatus().Status)
}
