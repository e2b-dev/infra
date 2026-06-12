package nodemanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type recordingInfoClient struct {
	orchestratorinfo.InfoServiceClient

	req   *orchestratorinfo.ServiceStatusChangeRequest
	calls int
}

func (r *recordingInfoClient) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	r.calls++
	r.req = req

	return &emptypb.Empty{}, nil
}

func TestSendStatusChangeMapsStatusesAndForwardsForceStop(t *testing.T) {
	t.Parallel()

	cases := map[api.NodeStatus]orchestratorinfo.ServiceInfoStatus{
		api.NodeStatusReady:     orchestratorinfo.ServiceInfoStatus_Healthy,
		api.NodeStatusDraining:  orchestratorinfo.ServiceInfoStatus_Draining,
		api.NodeStatusUnhealthy: orchestratorinfo.ServiceInfoStatus_Unhealthy,
		api.NodeStatusStandby:   orchestratorinfo.ServiceInfoStatus_Standby,
	}
	require.Len(t, ApiNodeToOrchestratorStateMapper, len(cases))

	for nodeStatus, expectedServiceStatus := range cases {
		t.Run(string(nodeStatus), func(t *testing.T) {
			t.Parallel()

			node := NewTestNode("node-id", api.NodeStatusReady, 0, 4)
			info := &recordingInfoClient{}
			node.client.Info = info
			forceStop := nodeStatus == api.NodeStatusDraining

			err := node.SendStatusChange(t.Context(), nodeStatus, forceStop)
			require.NoError(t, err)
			require.Equal(t, 1, info.calls)
			require.NotNil(t, info.req)
			require.Equal(t, expectedServiceStatus, info.req.GetServiceStatus())
			require.Equal(t, forceStop, info.req.GetForceStop())
		})
	}
}

func TestSendStatusChangeUnknownStatusDoesNotCallClient(t *testing.T) {
	t.Parallel()

	node := NewTestNode("node-id", api.NodeStatusReady, 0, 4)
	info := &recordingInfoClient{}
	node.client.Info = info

	err := node.SendStatusChange(t.Context(), api.NodeStatusConnecting, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown service info status: connecting")
	require.Zero(t, info.calls)
	require.Nil(t, info.req)
}
