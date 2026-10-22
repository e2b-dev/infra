package orchestrator

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Node versions as orchestrator-ee reports them: the build that added
// egress_proxy_tls, and the one just before it.
const (
	buildWithEgressProxyTLS   = "0.16.202610011012-6bbb0090f80-ee"
	buildBeforeEgressProxyTLS = "0.16.202610011007-5dfa8dfb8aa-ee"
)

func addRunningSandbox(t *testing.T, o *Orchestrator) sandbox.Sandbox {
	t.Helper()

	return addSandboxInState(t, o, sandbox.StateRunning, "node-1")
}

func addSandboxInState(t *testing.T, o *Orchestrator, state sandbox.State, nodeID string) sandbox.Sandbox {
	t.Helper()

	sbx := sandbox.Sandbox{
		SandboxID:         "sbx-" + uuid.NewString()[:8],
		TemplateID:        "tpl-1",
		ExecutionID:       uuid.NewString(),
		TeamID:            uuid.New(),
		BuildID:           uuid.New(),
		MaxInstanceLength: time.Hour,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		VCpu:              2,
		RamMB:             512,
		NodeID:            nodeID,
		ClusterID:         uuid.Nil,
		State:             state,
	}
	require.NoError(t, o.sandboxStore.Add(t.Context(), sbx, nil))

	return sbx
}

func tlsProxy() *sandbox_network.EgressProxyConfig {
	return &sandbox_network.EgressProxyConfig{
		Address: "proxy.example.com:1080",
		TLS:     &sandbox_network.EgressProxyTLSConfig{Enabled: true},
	}
}

func TestUpdateSandboxNetworkConfig_PlacedNetworkRefusesProxyBeforeStore(t *testing.T) {
	t.Parallel()
	o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildWithEgressProxyTLS))
	sbx := addRunningSandbox(t, o)
	_, err := o.sandboxStore.Update(t.Context(), sbx.TeamID, sbx.SandboxID, func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		sbx.NetworkPlacement = &orchestratorgrpc.SandboxNetworkPlacement{NetworkMode: "egress-router"}
		sbx.Network = &types.SandboxNetworkConfig{Egress: &types.SandboxNetworkEgressConfig{AllowedAddresses: []string{"example.com"}}}

		return sbx, nil
	})
	require.NoError(t, err)

	apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil, tlsProxy())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	stored, err := o.sandboxStore.Get(t.Context(), sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, &types.SandboxNetworkEgressConfig{AllowedAddresses: []string{"example.com"}}, stored.Network.Egress,
		"a rejected proxy must not replace the API's committed configuration")

	require.Nil(t, o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil, nil),
		"removing the proxy is still accepted on a routed execution")
}

// A live network update never goes through placement, so the node the sandbox
// already runs on has to be checked for the capability instead.
func TestUpdateSandboxNetworkConfig_EgressProxyTLSGate(t *testing.T) {
	t.Parallel()

	t.Run("old node refuses tls and stores nothing", func(t *testing.T) {
		t.Parallel()

		o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildBeforeEgressProxyTLS))
		sbx := addRunningSandbox(t, o)

		apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil, tlsProxy())
		require.NotNil(t, apiErr, "a node that would drop the TLS block must not be sent it")
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.Code)
		assert.Equal(t, errCodeFeatureUnsupported, apiErr.ErrorCode)

		stored, err := o.sandboxStore.Get(t.Context(), sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.False(t, stored.Network.HasEgressProxyTLS(),
			"a refused config must not be stored, or GET would report TLS the hop does not use")
	})

	t.Run("new node accepts tls", func(t *testing.T) {
		t.Parallel()

		o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildWithEgressProxyTLS))
		sbx := addRunningSandbox(t, o)

		apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil, tlsProxy())
		require.Nil(t, apiErr)

		stored, err := o.sandboxStore.Get(t.Context(), sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.True(t, stored.Network.HasEgressProxyTLS())
	})

	t.Run("old node still accepts a proxy without tls", func(t *testing.T) {
		t.Parallel()

		o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildBeforeEgressProxyTLS))
		sbx := addRunningSandbox(t, o)

		apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil,
			&sandbox_network.EgressProxyConfig{Address: "proxy.example.com:1080"})
		require.Nil(t, apiErr, "the gate applies only to configs that ask for TLS")
	})

	t.Run("sandbox that is not running refuses tls and stores nothing", func(t *testing.T) {
		t.Parallel()

		o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildBeforeEgressProxyTLS))
		sbx := addSandboxInState(t, o, sandbox.StateSnapshotting, "node-1")

		apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil, tlsProxy())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)

		stored, err := o.sandboxStore.Get(t.Context(), sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.False(t, stored.Network.HasEgressProxyTLS())
	})

	t.Run("node missing from the cache refuses tls and stores nothing", func(t *testing.T) {
		t.Parallel()

		o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildWithEgressProxyTLS))
		sbx := addSandboxInState(t, o, sandbox.StateRunning, "node-unknown")

		apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID, nil, nil, nil, nil, nil, tlsProxy())
		require.NotNil(t, apiErr, "a node whose version cannot be read must not be sent TLS")
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.Code)

		stored, err := o.sandboxStore.Get(t.Context(), sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.False(t, stored.Network.HasEgressProxyTLS())
	})

	t.Run("missing sandbox is not found", func(t *testing.T) {
		t.Parallel()

		o := newCreateSandboxTestOrchestrator(t, nodemanager.WithOrchestratorVersion(buildWithEgressProxyTLS))

		apiErr := o.UpdateSandboxNetworkConfig(t.Context(), uuid.New(), "sbx-missing", nil, nil, nil, nil, nil, tlsProxy())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})
}

type capturingSandboxUpdateClient struct {
	orchestratorgrpc.SandboxServiceClient

	req *orchestratorgrpc.SandboxUpdateRequest
}

func (c *capturingSandboxUpdateClient) Update(_ context.Context, req *orchestratorgrpc.SandboxUpdateRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	c.req = req

	return &emptypb.Empty{}, nil
}

// An update replaces dnsServers like every other egress field: leaving it out
// resets it to the default, which allows the default nameserver.
func TestUpdateSandboxNetworkConfig_DnsServers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		stored         *[]string
		update         *[]string
		wantStored     *[]string
		wantNameserver bool
	}{
		{"omitted resets an empty list to the default", &[]string{}, nil, nil, true},
		{"listed default server reopens it", &[]string{}, &[]string{"8.8.8.8"}, &[]string{"8.8.8.8"}, true},
		{"empty list closes the default nameserver", nil, &[]string{}, &[]string{}, false},
		{"other server replaces the default", nil, &[]string{"1.1.1.1"}, &[]string{"1.1.1.1"}, false},
		{"omitted keeps the default", nil, nil, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sbx := sandbox.Sandbox{
				SandboxID: "sbx-dns", TeamID: uuid.New(), NodeID: "node-dns",
				ClusterID: uuid.Nil, State: sandbox.StateRunning,
				StartTime: time.Now(), EndTime: time.Now().Add(time.Hour), MaxInstanceLength: time.Hour,
				Network: &types.SandboxNetworkConfig{Egress: &types.SandboxNetworkEgressConfig{
					AllowedAddresses: []string{"example.com"},
					DeniedAddresses:  []string{sandbox_network.AllInternetTrafficCIDR},
					DnsServers:       tt.stored,
				}},
			}
			storage := &updateRejectionStorage{sbx: sbx}
			o := &Orchestrator{
				sandboxStore: sandbox.NewStore(storage, nil, sandbox.Callbacks{}),
				nodes:        smap.New[*nodemanager.Node](),
			}
			client := &capturingSandboxUpdateClient{}
			node := nodemanager.NewTestNode(sbx.NodeID, api.NodeStatusReady, 0, 2)
			node.ClusterID = sbx.ClusterID
			node.SetSandboxClient(client)
			o.registerNode(node)

			apiErr := o.UpdateSandboxNetworkConfig(t.Context(), sbx.TeamID, sbx.SandboxID,
				[]string{"example.com"}, []string{sandbox_network.AllInternetTrafficCIDR}, nil, nil, tt.update, nil)
			require.Nil(t, apiErr)

			require.NotNil(t, storage.sbx.Network)
			require.NotNil(t, storage.sbx.Network.Egress)
			assert.Equal(t, tt.wantStored, storage.sbx.Network.Egress.DnsServers)

			require.NotNil(t, client.req, "the update must reach the node")
			cidrs := client.req.GetEgress().GetAllowedCidrs()
			if tt.wantNameserver {
				assert.Contains(t, cidrs, defaultNameserverCIDR)
			} else {
				assert.NotContains(t, cidrs, defaultNameserverCIDR)
			}
			for _, server := range utils.DerefOrDefault(tt.update, nil) {
				assert.Contains(t, cidrs, server+"/32")
			}
		})
	}
}

// A checkpoint records the network of the execution it left running, as
// the node reports it, in the write that puts the record back to Running,
// so the proxy check follows the VM that runs. A sandbox the Router served,
// resumed in a new VM on the worker's network, takes a proxy afterwards.
// One placed on the Router again still refuses a proxy before anything is
// stored, and still takes its removal. A node that predates the report and
// a placement of another execution leave the recorded placement, and a
// refused checkpoint records nothing.
func TestCheckpointRecordsTheRunningNetwork(t *testing.T) {
	t.Parallel()

	proxy := &sandbox_network.EgressProxyConfig{Address: "proxy.example.com:1080"}
	for name, c := range checkpointCallers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, client := checkpointFixture(t, func(context.Context) error { return nil })
			placed := routedPlacement(f.sbx.ExecutionID)
			setPlacement(t, f, placed)
			checkpoint := func(response *orchestratorgrpc.SandboxCheckpointResponse) {
				t.Helper()
				client.response = response
				require.NoError(t, c.call(t.Context(), f.o, f.sbx.TeamID, f.sbx.SandboxID))
			}

			checkpoint(&orchestratorgrpc.SandboxCheckpointResponse{})
			assert.True(t, proto.Equal(placed, runningPlacement(t, f)), "a node that predates the report leaves the placement")
			checkpoint(reportedNetwork(routedPlacement(uuid.NewString())))
			assert.True(t, proto.Equal(placed, runningPlacement(t, f)), "a placement of another execution is refused")

			checkpoint(reportedNetwork(nil))
			require.Nil(t, runningPlacement(t, f), "the replacement runs on the worker's network")
			require.Nil(t, f.o.UpdateSandboxNetworkConfig(t.Context(), f.sbx.TeamID, f.sbx.SandboxID, nil, nil, nil, nil, nil, proxy),
				"the replacement takes a proxy")

			setPlacement(t, f, nil)
			checkpoint(reportedNetwork(placed))
			assert.True(t, proto.Equal(placed, runningPlacement(t, f)), "a replacement placed on the Router again is recorded")
			apiErr := f.o.UpdateSandboxNetworkConfig(t.Context(), f.sbx.TeamID, f.sbx.SandboxID, []string{"example.com"}, nil, nil, nil, nil, proxy)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code, "and refuses a proxy before anything is stored")
			stored, err := f.o.sandboxStore.Get(t.Context(), f.sbx.TeamID, f.sbx.SandboxID)
			require.NoError(t, err)
			assert.Equal(t, proxy.Address, stored.Network.Egress.EgressProxyAddress, "the stored configuration is the earlier one")
			require.Nil(t, f.o.UpdateSandboxNetworkConfig(t.Context(), f.sbx.TeamID, f.sbx.SandboxID, nil, nil, nil, nil, nil, nil), "and takes the proxy's removal")

			client.checkpoint = func(context.Context) error {
				return status.Error(codes.FailedPrecondition, "envd too old")
			}
			client.response = reportedNetwork(nil)
			require.Error(t, c.call(t.Context(), f.o, f.sbx.TeamID, f.sbx.SandboxID))
			assert.True(t, proto.Equal(placed, runningPlacement(t, f)), "a refused checkpoint records nothing")
		})
	}
}

// A completed checkpoint whose record cannot be written back leaves it
// Snapshotting with its old placement, instead of Running on a network its
// VM left, and says so: a proxy update is refused as a state change, not
// against the stale placement, and the sandbox is not killed. A successor
// that replaces the record just before the write keeps its record as it
// wrote it, and the completion changes nothing of it.
func TestCheckpointNeverServesAStalePlacement(t *testing.T) {
	t.Parallel()

	for name, c := range checkpointCallers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hook := &checkpointRestoreHook{}
			f, client := checkpointFixture(t, func(context.Context) error { return nil }, hook)
			placed := routedPlacement(f.sbx.ExecutionID)
			setPlacement(t, f, placed)
			client.response = reportedNetwork(nil)

			hook.fail = true
			hook.armed.Store(true)
			err := c.call(t.Context(), f.o, f.sbx.TeamID, f.sbx.SandboxID)
			require.ErrorContains(t, err, "the record store is unavailable")
			stored, err := f.o.sandboxStore.Get(t.Context(), f.sbx.TeamID, f.sbx.SandboxID)
			require.NoError(t, err)
			assert.Equal(t, sandbox.StateSnapshotting, stored.State)
			assert.True(t, proto.Equal(placed, stored.NetworkPlacement))
			assert.Zero(t, client.deletes.Load(), "the healthy VM is not killed")
			apiErr := f.o.UpdateSandboxNetworkConfig(t.Context(), f.sbx.TeamID, f.sbx.SandboxID, nil, nil, nil, nil, nil,
				&sandbox_network.EgressProxyConfig{Address: "proxy.example.com:1080"})
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusConflict, apiErr.Code)
			require.NoError(t, f.o.sandboxStore.WaitForStateChange(t.Context(), f.sbx.TeamID, f.sbx.SandboxID), "the transition ended")

			// The record goes back to Running for the next case, and a
			// successor replaces it just before the checkpoint's write.
			_, err = f.o.sandboxStore.Update(t.Context(), f.sbx.TeamID, f.sbx.SandboxID, func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
				sbx.State = sandbox.StateRunning

				return sbx, nil
			})
			require.NoError(t, err)
			successor := successorOf(f.sbx)
			successor.NetworkPlacement = routedPlacement(successor.ExecutionID)
			var written sandbox.Sandbox
			hook.fail = false
			hook.before = func(ctx context.Context) error {
				if err := f.o.sandboxStore.Add(ctx, successor, nil); err != nil {
					return err
				}
				var err error
				written, err = f.o.sandboxStore.Get(ctx, f.sbx.TeamID, f.sbx.SandboxID)

				return err
			}
			hook.armed.Store(true)
			require.NoError(t, c.call(t.Context(), f.o, f.sbx.TeamID, f.sbx.SandboxID))
			require.NoError(t, f.o.sandboxStore.WaitForStateChange(t.Context(), f.sbx.TeamID, f.sbx.SandboxID))
			after, err := f.o.sandboxStore.Get(t.Context(), f.sbx.TeamID, f.sbx.SandboxID)
			require.NoError(t, err)
			assert.Equal(t, successor.ExecutionID, after.ExecutionID)
			assert.Equal(t, written, after, "the successor's record is as it wrote it")
		})
	}
}

func TestCheckpointRestoreConflictAndStateChange(t *testing.T) {
	t.Parallel()

	for caller, c := range checkpointCallers {
		for _, state := range []sandbox.State{sandbox.StateSnapshotting, sandbox.StateKilling} {
			t.Run(caller+"/"+string(state), func(t *testing.T) {
				t.Parallel()

				hook := &checkpointRestoreHook{}
				f, client := checkpointFixture(t, func(context.Context) error { return nil }, hook)
				setPlacement(t, f, routedPlacement(f.sbx.ExecutionID))
				client.response = reportedNetwork(nil)
				var written sandbox.Sandbox
				rewrites := 0
				hook.before = func(ctx context.Context) error {
					var err error
					written, err = f.o.sandboxStore.Get(ctx, f.sbx.TeamID, f.sbx.SandboxID)
					if err != nil {
						return err
					}
					rewrites++
					written.State = state
					written.Metadata = map[string]string{"rewrite": strconv.Itoa(rewrites)}
					// Keep rewriting Snapshotting under both compares. Killing
					// models another operation taking over before restoration.
					hook.armed.Store(state == sandbox.StateSnapshotting)

					return f.o.sandboxStore.Add(ctx, written, nil)
				}
				hook.armed.Store(true)

				err := c.call(t.Context(), f.o, f.sbx.TeamID, f.sbx.SandboxID)
				if state == sandbox.StateSnapshotting {
					require.ErrorIs(t, err, sandbox.ErrRestoreConflict, "a failed restore must not report checkpoint success")
					assert.GreaterOrEqual(t, rewrites, 2, "both compare attempts failed")
				} else {
					require.NoError(t, err, "a completed checkpoint yields to the operation that took over")
				}
				stored, err := f.o.sandboxStore.Get(t.Context(), f.sbx.TeamID, f.sbx.SandboxID)
				require.NoError(t, err)
				assert.Equal(t, written, stored, "neither restoration nor completion overwrites the record")
				assert.Zero(t, client.deletes.Load(), "restoration never kills the VM")
				require.NoError(t, f.o.sandboxStore.WaitForStateChange(t.Context(), f.sbx.TeamID, f.sbx.SandboxID))
			})
		}
	}
}

func routedPlacement(execution string) *orchestratorgrpc.SandboxNetworkPlacement {
	return &orchestratorgrpc.SandboxNetworkPlacement{ExecutionId: execution, NetworkMode: "egress-router", RouterId: "router"}
}

func reportedNetwork(placement *orchestratorgrpc.SandboxNetworkPlacement) *orchestratorgrpc.SandboxCheckpointResponse {
	return &orchestratorgrpc.SandboxCheckpointResponse{Network: &orchestratorgrpc.SandboxCheckpointNetwork{Placement: placement}}
}

func setPlacement(t *testing.T, f refusalFixture, placement *orchestratorgrpc.SandboxNetworkPlacement) {
	t.Helper()
	_, err := f.o.sandboxStore.Update(t.Context(), f.sbx.TeamID, f.sbx.SandboxID, func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		sbx.NetworkPlacement = placement

		return sbx, nil
	})
	require.NoError(t, err)
}

// runningPlacement returns the placement of the fixture's record, which must
// be Running.
func runningPlacement(t *testing.T, f refusalFixture) *orchestratorgrpc.SandboxNetworkPlacement {
	t.Helper()
	stored, err := f.o.sandboxStore.Get(t.Context(), f.sbx.TeamID, f.sbx.SandboxID)
	require.NoError(t, err)
	require.Equal(t, sandbox.StateRunning, stored.State)

	return stored.NetworkPlacement
}
