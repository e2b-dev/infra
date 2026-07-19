//go:build linux

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestServiceStatusOverrideFencesRuntimeIdentity(t *testing.T) {
	info := &ServiceInfo{ClientId: "node-1", ServiceId: "service-new"}
	server := &Server{info: info}

	_, err := server.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-old",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, info.GetStatus())

	_, err = server.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-new",
	})
	require.NoError(t, err)
}
