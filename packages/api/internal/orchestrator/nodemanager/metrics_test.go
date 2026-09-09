package nodemanager

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestMetricsOutstandingWork(t *testing.T) {
	t.Parallel()

	n := &Node{}
	for _, work := range []*uint64{nil, new(uint64(0)), new(uint64(7)), nil} {
		n.UpdateMetricsFromServiceInfoResponse(&orchestratorinfo.ServiceInfoResponse{OutstandingWork: work})
		require.Equal(t, work, n.Metrics().OutstandingWork)
	}
}

func TestMetricsOutstandingWorkSnapshotIsolation(t *testing.T) {
	t.Parallel()

	n := &Node{}
	info := &orchestratorinfo.ServiceInfoResponse{OutstandingWork: new(uint64(7))}
	n.UpdateMetricsFromServiceInfoResponse(info)
	*info.OutstandingWork = 8
	require.Equal(t, new(uint64(7)), n.Metrics().OutstandingWork)

	snapshot := n.Metrics()
	*snapshot.OutstandingWork = 9
	require.Equal(t, new(uint64(7)), n.Metrics().OutstandingWork)

	snapshot = n.Metrics()
	n.UpdateMetricsFromServiceInfoResponse(info)
	require.Equal(t, new(uint64(7)), snapshot.OutstandingWork)
	require.Equal(t, new(uint64(8)), n.Metrics().OutstandingWork)

	n.UpdateMetricsFromServiceInfoResponse(&orchestratorinfo.ServiceInfoResponse{})
	require.Nil(t, n.Metrics().OutstandingWork)
	require.Equal(t, new(uint64(7)), snapshot.OutstandingWork)
}

func TestMetricsOutstandingWorkConcurrentSnapshots(t *testing.T) {
	t.Parallel()

	n := &Node{}
	work := new(uint64(7))
	info := &orchestratorinfo.ServiceInfoResponse{OutstandingWork: work}
	n.UpdateMetricsFromServiceInfoResponse(info)
	snapshot := n.Metrics()
	require.NotNil(t, snapshot.OutstandingWork)

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			*work++
			*snapshot.OutstandingWork++
		}
	})
	defer wg.Wait()

	for range 100 {
		require.Equal(t, new(uint64(7)), n.Metrics().OutstandingWork)
		n.UpdateMetricsFromServiceInfoResponse(&orchestratorinfo.ServiceInfoResponse{OutstandingWork: new(uint64(7))})
	}
}
