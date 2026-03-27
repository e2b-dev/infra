package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	sandboxredis "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type removeSandboxClientMock struct {
	grpcorchestrator.SandboxServiceClient

	deleteErr error
	pauseErr  error

	deleteCalls atomic.Int32
	pauseCalls  atomic.Int32

	onDeleteStart chan struct{}
	blockDelete   <-chan struct{}
}

func (m *removeSandboxClientMock) Delete(_ context.Context, _ *grpcorchestrator.SandboxDeleteRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	m.deleteCalls.Add(1)
	if m.onDeleteStart != nil {
		select {
		case m.onDeleteStart <- struct{}{}:
		default:
		}
	}

	if m.blockDelete != nil {
		<-m.blockDelete
	}

	if m.deleteErr != nil {
		return nil, m.deleteErr
	}

	return &emptypb.Empty{}, nil
}

func (m *removeSandboxClientMock) Pause(_ context.Context, _ *grpcorchestrator.SandboxPauseRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	m.pauseCalls.Add(1)
	if m.pauseErr != nil {
		return nil, m.pauseErr
	}

	return &emptypb.Empty{}, nil
}

func newTestRemoveSandboxOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()

	ctx := t.Context()
	logger.ReplaceGlobals(ctx, logger.NewNopLogger())

	analytics, err := analyticscollector.NewAnalytics(ctx, "", "")
	require.NoError(t, err)

	posthogClient, err := analyticscollector.NewPosthogClient(ctx, "")
	require.NoError(t, err)

	nomadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]*nomadapi.NodeListStub{})

			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(nomadServer.Close)

	nomadClient, err := nomadapi.NewClient(&nomadapi.Config{Address: nomadServer.URL})
	require.NoError(t, err)

	redisClient := redis_utils.SetupInstance(t)
	redisStorage := sandboxredis.NewStorage(redisClient)

	storageCtx, storageCancel := context.WithCancel(context.Background())
	go redisStorage.Start(storageCtx)

	o := &Orchestrator{
		sandboxStore: sandbox.NewStore(
			redisStorage,
			reservations.NewReservationStorage(),
			sandbox.Callbacks{
				AddSandboxToRoutingTable: func(context.Context, sandbox.Sandbox) {},
				AsyncNewlyCreatedSandbox: func(context.Context, sandbox.Sandbox) {},
			},
		),
		nodes:          smap.New[*nodemanager.Node](),
		nomadClient:    nomadClient,
		routingCatalog: e2bcatalog.NewMemorySandboxesCatalog(),
		analytics:      analytics,
		posthogClient:  posthogClient,
	}

	t.Cleanup(func() {
		storageCancel()
		redisStorage.Close()
		_ = o.routingCatalog.Close(context.Background())
		_ = posthogClient.Close()
		_ = analytics.Close()
	})

	return o
}

func testSandboxForRemove(teamID, clusterID uuid.UUID, nodeID, sandboxID string, state sandbox.State) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID:         sandboxID,
		ExecutionID:       "exec-1",
		TemplateID:        "tmpl-1",
		TeamID:            teamID,
		StartTime:         time.Now().Add(-time.Minute),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
		State:             state,
		NodeID:            nodeID,
		ClusterID:         clusterID,
	}
}

func addSandboxForRemove(t *testing.T, o *Orchestrator, sbx sandbox.Sandbox) {
	t.Helper()
	require.NoError(t, o.sandboxStore.Add(t.Context(), sbx, false))
}

func addNodeForRemove(o *Orchestrator, id string, clusterID uuid.UUID, sandboxClient grpcorchestrator.SandboxServiceClient) *nodemanager.TestNode {
	n := nodemanager.NewTestNode(id, api.NodeStatusReady, 2, 4)
	n.ClusterID = clusterID
	n.SetSandboxClient(sandboxClient)
	o.registerNode(n)

	return n
}

func assertSandboxState(t *testing.T, o *Orchestrator, teamID uuid.UUID, sandboxID string, expected sandbox.State) {
	t.Helper()

	stored, err := o.sandboxStore.Get(t.Context(), teamID, sandboxID)
	require.NoError(t, err)
	assert.Equal(t, expected, stored.State)
}

func assertSandboxMissing(t *testing.T, o *Orchestrator, teamID uuid.UUID, sandboxID string) {
	t.Helper()

	_, err := o.sandboxStore.Get(t.Context(), teamID, sandboxID)
	require.Error(t, err)
	assert.ErrorIs(t, err, sandbox.ErrNotFound)
}

func TestRemoveSandbox_NotFound(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()

	t.Run("kill action", func(t *testing.T) {
		err := o.RemoveSandbox(t.Context(), teamID, "missing-kill", sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSandboxNotFound)
	})

	t.Run("pause action", func(t *testing.T) {
		err := o.RemoveSandbox(t.Context(), teamID, "missing-pause", sandbox.RemoveOpts{Action: sandbox.StateActionPause})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSandboxNotFound)
	})
}

func TestRemoveSandbox_InvalidAction(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	sbx := testSandboxForRemove(teamID, uuid.New(), "node-1", "sbx-invalid-action", sandbox.StateRunning)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{
		Action: sandbox.StateAction{Name: "invalid-action", TargetState: sandbox.State("not-a-real-state")},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxOperationFailed)
	assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateRunning)
}

func TestRemoveSandbox_AlreadyKilling_ReturnsNil(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	sbx := testSandboxForRemove(teamID, uuid.New(), "node-1", "sbx-already-killing", sandbox.StateKilling)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	require.NoError(t, err)
	assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateKilling)
}

func TestRemoveSandbox_PauseWhenKilling_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	sbx := testSandboxForRemove(teamID, uuid.New(), "node-1", "sbx-killing-pause", sandbox.StateKilling)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxNotFound)
	assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateKilling)
}

func TestRemoveSandbox_Kill_NodeMissing_ReturnsOperationFailed(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	clusterID := consts.LocalClusterID
	sbx := testSandboxForRemove(teamID, clusterID, "missing-node", "sbx-missing-node", sandbox.StateRunning)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxOperationFailed)
	assertSandboxMissing(t, o, teamID, sbx.SandboxID)
}

func TestRemoveSandbox_Pause_NodeMissing_ReturnsOperationFailed(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	clusterID := consts.LocalClusterID
	sbx := testSandboxForRemove(teamID, clusterID, "missing-node", "sbx-pause-node-missing", sandbox.StateRunning)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxOperationFailed)
	assertSandboxMissing(t, o, teamID, sbx.SandboxID)
}

func TestRemoveSandbox_Kill_DeleteRPCError_ReturnsOperationFailed(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	clusterID := uuid.New()

	sandboxClient := &removeSandboxClientMock{deleteErr: errors.New("delete failed")}
	addNodeForRemove(o, "node-delete-err", clusterID, sandboxClient)

	sbx := testSandboxForRemove(teamID, clusterID, "node-delete-err", "sbx-delete-rpc-error", sandbox.StateRunning)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxOperationFailed)
	assert.Equal(t, int32(1), sandboxClient.deleteCalls.Load())
	assertSandboxMissing(t, o, teamID, sbx.SandboxID)
}

func TestRemoveSandbox_Kill_Success_RemovesSandbox(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	clusterID := uuid.New()

	sandboxClient := &removeSandboxClientMock{}
	addNodeForRemove(o, "node-delete-ok", clusterID, sandboxClient)

	sbx := testSandboxForRemove(teamID, clusterID, "node-delete-ok", "sbx-delete-success", sandbox.StateRunning)
	addSandboxForRemove(t, o, sbx)

	err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	require.NoError(t, err)
	assert.Equal(t, int32(1), sandboxClient.deleteCalls.Load())
	assertSandboxMissing(t, o, teamID, sbx.SandboxID)
}

func TestRemoveSandbox_ConcurrentKill_SharesTransition(t *testing.T) {
	t.Parallel()

	o := newTestRemoveSandboxOrchestrator(t)
	teamID := uuid.New()
	clusterID := uuid.New()

	releaseDelete := make(chan struct{})
	sandboxClient := &removeSandboxClientMock{
		onDeleteStart: make(chan struct{}, 1),
		blockDelete:   releaseDelete,
	}
	addNodeForRemove(o, "node-concurrent-delete", clusterID, sandboxClient)

	sbx := testSandboxForRemove(teamID, clusterID, "node-concurrent-delete", "sbx-concurrent-kill", sandbox.StateRunning)
	addSandboxForRemove(t, o, sbx)

	var wg sync.WaitGroup
	var errA error
	var errB error

	wg.Add(1)
	go func() {
		defer wg.Done()
		errA = o.RemoveSandbox(context.Background(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	}()

	select {
	case <-sandboxClient.onDeleteStart:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first delete call")
	}

	assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateKilling)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errB = o.RemoveSandbox(context.Background(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	}()

	close(releaseDelete)
	wg.Wait()

	assert.True(t, errA == nil || errors.Is(errA, ErrSandboxNotFound))
	assert.True(t, errB == nil || errors.Is(errB, ErrSandboxNotFound))
	assert.True(t, errA == nil || errB == nil)
	assert.Equal(t, int32(1), sandboxClient.deleteCalls.Load())
	assertSandboxMissing(t, o, teamID, sbx.SandboxID)
}

func TestRemoveSandbox_ExpectedSemanticsMatrix(t *testing.T) {
	t.Parallel()

	t.Run("sandbox not found returns ErrSandboxNotFound", func(t *testing.T) {
		o := newTestRemoveSandboxOrchestrator(t)
		err := o.RemoveSandbox(t.Context(), uuid.New(), "missing", sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSandboxNotFound)
	})

	t.Run("invalid action keeps state unchanged", func(t *testing.T) {
		o := newTestRemoveSandboxOrchestrator(t)
		teamID := uuid.New()
		sbx := testSandboxForRemove(teamID, uuid.New(), "node-1", "matrix-invalid-action", sandbox.StateRunning)
		addSandboxForRemove(t, o, sbx)

		err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{
			Action: sandbox.StateAction{Name: "invalid-action", TargetState: sandbox.State("invalid")},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSandboxOperationFailed)
		assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateRunning)
	})

	t.Run("successful kill removes sandbox", func(t *testing.T) {
		o := newTestRemoveSandboxOrchestrator(t)
		teamID := uuid.New()
		clusterID := uuid.New()
		client := &removeSandboxClientMock{}
		addNodeForRemove(o, "matrix-node-delete-ok", clusterID, client)

		sbx := testSandboxForRemove(teamID, clusterID, "matrix-node-delete-ok", "matrix-sbx-delete-success", sandbox.StateRunning)
		addSandboxForRemove(t, o, sbx)

		err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		require.NoError(t, err)
		assertSandboxMissing(t, o, teamID, sbx.SandboxID)
	})

	t.Run("node missing should preserve sandbox for retry (target behavior)", func(t *testing.T) {
		t.Skip("target semantics: currently sandbox is removed on operation failure")

		o := newTestRemoveSandboxOrchestrator(t)
		teamID := uuid.New()
		sbx := testSandboxForRemove(teamID, consts.LocalClusterID, "missing-node", "matrix-sbx-node-missing", sandbox.StateRunning)
		addSandboxForRemove(t, o, sbx)

		err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSandboxOperationFailed)
		assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateKilling)
	})

	t.Run("delete RPC failure should preserve sandbox for retry (target behavior)", func(t *testing.T) {
		t.Skip("target semantics: currently sandbox is removed on operation failure")

		o := newTestRemoveSandboxOrchestrator(t)
		teamID := uuid.New()
		clusterID := uuid.New()
		client := &removeSandboxClientMock{deleteErr: errors.New("delete failed")}
		addNodeForRemove(o, "matrix-node-delete-fail", clusterID, client)

		sbx := testSandboxForRemove(teamID, clusterID, "matrix-node-delete-fail", "matrix-sbx-delete-fail", sandbox.StateRunning)
		addSandboxForRemove(t, o, sbx)

		err := o.RemoveSandbox(t.Context(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSandboxOperationFailed)
		assertSandboxState(t, o, teamID, sbx.SandboxID, sandbox.StateKilling)
	})

	t.Run("concurrent kill should be fully idempotent (target behavior)", func(t *testing.T) {
		t.Skip("target semantics: second concurrent call can currently return ErrSandboxNotFound")

		o := newTestRemoveSandboxOrchestrator(t)
		teamID := uuid.New()
		clusterID := uuid.New()

		releaseDelete := make(chan struct{})
		client := &removeSandboxClientMock{
			onDeleteStart: make(chan struct{}, 1),
			blockDelete:   releaseDelete,
		}
		addNodeForRemove(o, "matrix-node-concurrent", clusterID, client)

		sbx := testSandboxForRemove(teamID, clusterID, "matrix-node-concurrent", "matrix-sbx-concurrent", sandbox.StateRunning)
		addSandboxForRemove(t, o, sbx)

		var wg sync.WaitGroup
		var errA error
		var errB error

		wg.Add(1)
		go func() {
			defer wg.Done()
			errA = o.RemoveSandbox(context.Background(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		}()

		select {
		case <-client.onDeleteStart:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first delete call")
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			errB = o.RemoveSandbox(context.Background(), teamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		}()

		close(releaseDelete)
		wg.Wait()

		require.NoError(t, errA)
		require.NoError(t, errB)
		assertSandboxMissing(t, o, teamID, sbx.SandboxID)
	})
}
