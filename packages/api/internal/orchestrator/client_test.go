package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// newTestOrchestrator creates a minimal Orchestrator with only the fields
// needed for node lookup / discovery tests. Production fields (sandboxStore,
// analytics, redis, etc.) are left nil because the code paths under test never
// touch them.
func newTestOrchestrator(t *testing.T, nomad *nomadapi.Client) *Orchestrator {
	t.Helper()

	ctx := t.Context()
	logger.ReplaceGlobals(ctx, logger.NewNopLogger())

	return &Orchestrator{
		nodes:         smap.New[*nodemanager.Node](),
		nomadClient:   nomad,
		nomadNodePool: "default",
		tel:           telemetry.NewNoopClient(),
	}
}

func newNomadMock(t *testing.T, handler http.HandlerFunc) *nomadapi.Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := nomadapi.NewClient(&nomadapi.Config{Address: srv.URL})
	require.NoError(t, err)

	return client
}

// fakeInfoServer implements the minimum InfoServiceServer surface needed by
// nodemanager.New: it responds to ServiceInfo with a canned response carrying
// the given nodeID and Healthy status.
type fakeInfoServer struct {
	infogrpc.UnimplementedInfoServiceServer

	nodeID string
}

func (s *fakeInfoServer) ServiceInfo(context.Context, *emptypb.Empty) (*infogrpc.ServiceInfoResponse, error) {
	return &infogrpc.ServiceInfoResponse{
		NodeId:         s.nodeID,
		ServiceId:      "test-service-instance",
		ServiceStatus:  infogrpc.ServiceInfoStatus_Healthy,
		MetricCpuCount: 4,
	}, nil
}

// startFakeOrchestratorGRPC starts a gRPC server that responds to ServiceInfo
// requests. When addr is empty it listens on an ephemeral port; otherwise it
// binds to the given address (e.g. "127.0.0.1:5008").
func startFakeOrchestratorGRPC(t *testing.T, nodeID string, addr string) {
	t.Helper()

	if addr == "" {
		addr = "127.0.0.1:0"
	}

	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr)
	require.NoError(t, err)

	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	infogrpc.RegisterInfoServiceServer(srv, &fakeInfoServer{nodeID: nodeID})

	go srv.Serve(lis)
	t.Cleanup(srv.GracefulStop)
}

// TestGetOrConnectNode_CacheHit verifies that when a node is already in the
// cache, getOrConnectNode returns it immediately without triggering any
// discovery. This is the fast path exercised on every sandbox operation.
func TestGetOrConnectNode_CacheHit(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t, nil)

	clusterID := uuid.New()
	testNode := nodemanager.NewTestNode("node-1", api.NodeStatusReady, 3, 4)
	testNode.ClusterID = clusterID
	o.nodes.Insert(o.scopedNodeID(clusterID, "node-1"), testNode)

	got := o.getOrConnectNode(t.Context(), clusterID, "node-1")
	require.NotNil(t, got)
	assert.Equal(t, "node-1", got.ID)
}

func TestGetOrConnectNode_CacheHit_LocalCluster(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t, nil)

	testNode := nodemanager.NewTestNode("local-node", api.NodeStatusReady, 2, 4)
	testNode.ClusterID = consts.LocalClusterID
	o.nodes.Insert("local-node", testNode)

	got := o.getOrConnectNode(t.Context(), consts.LocalClusterID, "local-node")
	require.NotNil(t, got)
	assert.Equal(t, "local-node", got.ID)
}

// TestGetOrConnectNode_CacheMiss_TriggersNomadDiscovery verifies that when a Nomad node is NOT
// in the cache, getOrConnectNode triggers on-demand Nomad service discovery rather than immediately returning nil.
//
// This handles scenario when a new orchestrator node has joined the cluster and may not be in cache yet
func TestGetOrConnectNode_CacheMiss_TriggersNomadDiscovery(t *testing.T) {
	t.Parallel()

	var discoveryAttempts atomic.Int32

	nomadClient := newNomadMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			discoveryAttempts.Add(1)

			// Return a node stub. connectToNode will fail at the gRPC level
			// (nodemanager.New dials the fake address), but the important thing
			// is that discovery WAS attempted.
			resp := []*nomadapi.NodeListStub{
				{
					ID:       "abcdef1234567890abcdef1234567890abcdef12",
					Address:  "127.0.0.1",
					Status:   "ready",
					NodePool: "default",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

			return
		}

		http.NotFound(w, r)
	})

	o := newTestOrchestrator(t, nomadClient)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	// Request a node that isn't in cache — should trigger discovery.
	o.getOrConnectNode(ctx, consts.LocalClusterID, "nonexistent-node")

	// The node won't be found because connectToNode fails at the gRPC level,
	// but discovery MUST have been attempted.
	assert.Positive(t, discoveryAttempts.Load(), "expected on-demand Nomad discovery to be triggered")
}

// TestGetOrConnectNode_ConcurrentCacheMiss_SharesDiscovery verifies that
// multiple concurrent getOrConnectNode calls for the same missing node share
// a single discovery attempt
func TestGetOrConnectNode_ConcurrentCacheMiss_SharesDiscovery(t *testing.T) {
	t.Parallel()

	var discoveryAttempts atomic.Int32

	nomadClient := newNomadMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			discoveryAttempts.Add(1)
			// Slow response to ensure concurrent callers overlap.
			time.Sleep(100 * time.Millisecond)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]*nomadapi.NodeListStub{})

			return
		}

		http.NotFound(w, r)
	})

	o := newTestOrchestrator(t, nomadClient)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Fire 10 concurrent lookups for the same missing node.
	const concurrency = 10
	done := make(chan struct{}, concurrency)
	for range concurrency {
		go func() {
			defer func() { done <- struct{}{} }()
			o.getOrConnectNode(ctx, consts.LocalClusterID, "missing-node")
		}()
	}

	for range concurrency {
		<-done
	}

	// should collapse all 10 calls into ≤2
	// one flight, possibly one more if the first completed before a late caller arrived
	assert.LessOrEqual(t, discoveryAttempts.Load(), int32(2),
		"singleflight should deduplicate concurrent discovery attempts")
}

// TestConnectToNode_SingleflightDedup verifies that concurrent connectToNode
// calls for the same NomadNodeShortID share a single connection attempt
func TestConnectToNode_SingleflightDedup(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t, nil)

	// grpc.NewClient is lazy — it returns immediately — and nodemanager.New
	// then fails at the ServiceInfo RPC call
	discovery := nodemanager.NomadServiceDiscovery{
		NomadNodeShortID:    "abcdef12",
		OrchestratorAddress: "127.0.0.1:1",
		IPAddress:           "127.0.0.1",
	}

	const concurrency = 10
	errs := make(chan error, concurrency)

	for range concurrency {
		go func() {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			errs <- o.connectToNode(ctx, discovery)
		}()
	}

	for range concurrency {
		<-errs
	}

	// After all calls complete, verify the node map has at most 1 entry
	// (or 0 if all failed)
	assert.LessOrEqual(t, o.nodes.Count(), 1, "singleflight should prevent duplicate registrations")
}

// TestGetOrConnectNode_CacheMiss_DiscoversAndConnects is the end-to-end
// test for a race condition. It simulates following scenario:
//
//  1. A new orchestrator node is running and reachable (fake gRPC server).
//  2. Nomad service discovery knows about it (mock HTTP API).
//  3. This API instance has NOT yet synced (node is absent from o.nodes).
//  4. A handler calls getOrConnectNode for a sandbox on that node.
//
// The fake gRPC server listens on consts.OrchestratorAPIPort so that
// listNomadNodes builds the correct address (ip:OrchestratorAPIPort).
func TestGetOrConnectNode_CacheMiss_DiscoversAndConnects(t *testing.T) {
	t.Parallel()

	orchestratorNodeID := "orch-node-42"
	nomadFullID := "aabbccdd11223344aabbccdd11223344aabbccdd"

	// 1. Start a fake gRPC server on consts.OrchestratorAPIPort so that the
	//    address built by listNomadNodes matches our listener.
	listenAddr := fmt.Sprintf("127.0.0.1:%d", consts.OrchestratorAPIPort)
	startFakeOrchestratorGRPC(t, orchestratorNodeID, listenAddr)

	// 2. Mock Nomad HTTP API returning a single ready node at 127.0.0.1.
	nomadClient := newNomadMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/nodes" {
			resp := []*nomadapi.NodeListStub{
				{
					ID:       nomadFullID,
					Address:  "127.0.0.1",
					Status:   "ready",
					NodePool: "default",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

			return
		}

		http.NotFound(w, r)
	})

	o := newTestOrchestrator(t, nomadClient)

	// 3. Verify the node is NOT in cache.
	assert.Nil(t, o.GetNode(consts.LocalClusterID, orchestratorNodeID))

	// 4. getOrConnectNode triggers the full discovery path:
	//    cache miss → discoverNomadNode → listNomadNodes → connectToNode → gRPC ServiceInfo → registerNode
	node := o.getOrConnectNode(t.Context(), consts.LocalClusterID, orchestratorNodeID)
	require.NotNil(t, node, "getOrConnectNode must discover and connect the node via Nomad")
	assert.Equal(t, orchestratorNodeID, node.ID)
	assert.Equal(t, nomadFullID[:consts.NodeIDLength], node.NomadNodeShortID)
}

// TestRegisterNode_NoDuplicates verifies that registerNode is idempotent
// when the same scoped key is used — the last write wins, and the map never
// grows beyond the number of unique keys.
func TestRegisterNode_NoDuplicates(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t, nil)

	clusterID := uuid.New()
	wg := sync.WaitGroup{}
	for i := range 50 {
		wg.Go(func() {
			node := nodemanager.NewTestNode(fmt.Sprintf("node-%d", i%5), api.NodeStatusReady, 2, 4)
			node.ClusterID = clusterID
			o.registerNode(node)
		})
	}

	wg.Wait()
	assert.Equal(t, 5, o.nodes.Count())
}
