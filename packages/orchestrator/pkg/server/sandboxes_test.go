//go:build linux

package server

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	startTime = time.Now()
	endTime   = time.Now().Add(time.Hour)
)

func Test_server_List(t *testing.T) {
	t.Parallel()
	type args struct {
		in1 *emptypb.Empty
	}
	tests := []struct {
		name    string
		args    args
		want    *orchestrator.SandboxListResponse
		wantErr bool
		data    []*sandbox.Sandbox
		endAt   time.Time
	}{
		{
			name: "should return all sandboxes",

			args: args{
				in1: &emptypb.Empty{},
			},
			data: []*sandbox.Sandbox{
				{
					APIStoredConfig: &orchestrator.SandboxConfig{
						TemplateId: "template-id",
					},
					Metadata: &sandbox.Metadata{
						Runtime: sandbox.RuntimeMetadata{
							SandboxID: id.Generate(),
						},
						Config: sandbox.NewConfig(sandbox.Config{}),
					},
					Resources: &sandbox.Resources{
						Slot: &network.Slot{HostIP: net.IPv4(127, 0, 0, 1)},
					},
				},
			},
			endAt: endTime,
			want: &orchestrator.SandboxListResponse{
				Sandboxes: []*orchestrator.RunningSandbox{
					{
						Config: &orchestrator.SandboxConfig{TemplateId: "template-id"},
						// ClientId:  "client-id",
						StartTime: timestamppb.New(startTime),
						EndTime:   timestamppb.New(endTime),
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sandboxes := sandbox.NewSandboxesMap()
			s := &Server{
				sandboxFactory: &sandbox.Factory{Sandboxes: sandboxes},
				info:           &service.ServiceInfo{},
			}
			for _, sbx := range tt.data {
				sbx.SetStartedAt(startTime)
				sbx.SetEndAt(tt.endAt)
				sandboxes.AssignNetwork(t.Context(), sbx)
				sandboxes.MarkRunning(t.Context(), sbx)
			}
			got, err := s.List(t.Context(), tt.args.in1)
			if (err != nil) != tt.wantErr {
				t.Errorf("server.List() error = %v, wantErr %v", err, tt.wantErr)

				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("server.List() = %v, want %v", got, tt.want)
			}
		})
	}
}

// newListSandbox builds a running-sandbox stand-in the way Create does: the
// live config gets its own clone of the request network, so the create-time
// APIStoredConfig snapshot and sbx.Config never share egress pointers.
func newListSandbox(t *testing.T, stored *orchestrator.SandboxConfig) *sandbox.Sandbox {
	t.Helper()

	return &sandbox.Sandbox{
		APIStoredConfig: stored,
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{SandboxID: id.Generate()},
			Config:  sandbox.NewConfig(sandbox.Config{Network: proto.CloneOf(stored.GetNetwork())}),
		},
		Resources: &sandbox.Resources{
			Slot: &network.Slot{HostIP: net.IPv4(127, 0, 0, 1)},
		},
	}
}

// listOneConfig registers a single sandbox and returns the config List reports.
func listOneConfig(t *testing.T, sbx *sandbox.Sandbox) *orchestrator.SandboxConfig {
	t.Helper()

	sbx.SetStartedAt(startTime)
	sbx.SetEndAt(endTime)

	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.AssignNetwork(t.Context(), sbx)
	sandboxes.MarkRunning(t.Context(), sbx)

	s := &Server{
		sandboxFactory: &sandbox.Factory{Sandboxes: sandboxes},
		info:           &service.ServiceInfo{},
	}

	got, err := s.List(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, got.GetSandboxes(), 1)

	return got.GetSandboxes()[0].GetConfig()
}

// Test_server_List_ReportsCurrentEgress covers EN-717: a network update
// publishes the new egress to sbx.Config, never to the create-time
// APIStoredConfig snapshot, so List has to report the live value. Otherwise an
// API re-sync repopulates its store with the stale one and the next
// pause/resume rebuilds the sandbox from it.
func Test_server_List_ReportsCurrentEgress(t *testing.T) {
	t.Parallel()

	t.Run("reports the updated egress without mutating the stored config", func(t *testing.T) {
		t.Parallel()

		stored := &orchestrator.SandboxConfig{
			TemplateId: "template-id",
			Network: &orchestrator.SandboxNetworkConfig{
				Egress:  &orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"1.0.0.0/8"}},
				Ingress: &orchestrator.SandboxNetworkIngressConfig{MaskRequestHost: new("example.test")},
			},
		}

		sbx := newListSandbox(t, stored)
		sbx.Config.SetNetworkEgress(&orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"9.9.9.9/32"}})

		got := listOneConfig(t, sbx)

		assert.Equal(t, []string{"9.9.9.9/32"}, got.GetNetwork().GetEgress().GetAllowedCidrs())
		// Everything else survives the overlay.
		assert.Equal(t, "template-id", got.GetTemplateId())
		assert.Equal(t, "example.test", got.GetNetwork().GetIngress().GetMaskRequestHost())
		// The stored config is shared and handed out by reference elsewhere, so
		// the overlay must have gone to a copy.
		assert.Equal(t, []string{"1.0.0.0/8"}, stored.GetNetwork().GetEgress().GetAllowedCidrs())
	})

	t.Run("reports egress cleared back to nil", func(t *testing.T) {
		t.Parallel()

		stored := &orchestrator.SandboxConfig{
			Network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"1.0.0.0/8"}},
			},
		}

		sbx := newListSandbox(t, stored)
		// An all-empty update collapses to nil (see applyNetworkEgress).
		sbx.Config.SetNetworkEgress(nil)

		got := listOneConfig(t, sbx)

		assert.Nil(t, got.GetNetwork().GetEgress())
		assert.Equal(t, []string{"1.0.0.0/8"}, stored.GetNetwork().GetEgress().GetAllowedCidrs())
	})

	t.Run("returns the stored config untouched when nothing changed", func(t *testing.T) {
		t.Parallel()

		stored := &orchestrator.SandboxConfig{
			Network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"1.0.0.0/8"}},
			},
		}

		got := listOneConfig(t, newListSandbox(t, stored))

		// No divergence, no clone.
		assert.Same(t, stored, got)
	})

	t.Run("clears a stale allow_internet_access=false once the deny-all is gone", func(t *testing.T) {
		t.Parallel()

		// How the API models allow_internet_access=false at create time.
		stored := &orchestrator.SandboxConfig{
			AllowInternetAccess: new(false),
			Network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					DeniedCidrs: []string{sandbox_network.AllInternetTrafficCIDR},
				},
			},
		}

		sbx := newListSandbox(t, stored)
		sbx.Config.SetNetworkEgress(&orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"1.2.3.4/32"}})

		got := listOneConfig(t, sbx)

		// Left at false, the API would fold it back into a deny-all on resume.
		assert.True(t, got.GetAllowInternetAccess())
		assert.False(t, stored.GetAllowInternetAccess())
	})

	t.Run("keeps allow_internet_access=false while the deny-all stands", func(t *testing.T) {
		t.Parallel()

		stored := &orchestrator.SandboxConfig{
			AllowInternetAccess: new(false),
			Network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					DeniedCidrs: []string{sandbox_network.AllInternetTrafficCIDR},
				},
			},
		}

		sbx := newListSandbox(t, stored)
		sbx.Config.SetNetworkEgress(&orchestrator.SandboxNetworkEgressConfig{
			AllowedCidrs: []string{"1.2.3.4/32"},
			DeniedCidrs:  []string{sandbox_network.AllInternetTrafficCIDR},
		})

		got := listOneConfig(t, sbx)

		assert.False(t, got.GetAllowInternetAccess())
	})

	t.Run("leaves an unset allow_internet_access unset", func(t *testing.T) {
		t.Parallel()

		// Nil means "never explicitly set" to the API; a deny-all reached the
		// egress some other way (denyOut 0.0.0.0/0) and carries itself.
		stored := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{}}

		sbx := newListSandbox(t, stored)
		sbx.Config.SetNetworkEgress(&orchestrator.SandboxNetworkEgressConfig{
			DeniedCidrs: []string{sandbox_network.AllInternetTrafficCIDR},
		})

		got := listOneConfig(t, sbx)

		//nolint:protogetter // unset must stay distinct from false
		assert.Nil(t, got.AllowInternetAccess)
	})
}

func TestGetSandboxExecutionData(t *testing.T) {
	t.Parallel()

	sbxStartedAt := time.Now().Add(-5 * time.Minute)

	sbx := &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.NewConfig(sandbox.Config{
				Vcpu:  2,
				RamMB: 512,
			}),
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: id.Generate(),
			},
		},
	}
	sbx.SetStartedAt(sbxStartedAt)

	s := &Server{}
	result := s.getSandboxExecutionData(sbx)

	assert.Equal(t, sbxStartedAt.UTC().Format(time.RFC3339), result["started_at"])
	assert.Equal(t, int64(2), result["vcpu_count"])
	assert.Equal(t, int64(512), result["memory_mb"])
	assert.IsType(t, int64(0), result["execution_time"])
	assert.Positive(t, result["execution_time"].(int64))
}

func TestAddKillReason(t *testing.T) {
	t.Parallel()

	t.Run("non-empty reason recorded", func(t *testing.T) {
		t.Parallel()

		eventData := map[string]any{}
		addKillReason(eventData, "request")

		assert.Equal(t, "request", eventData["kill_reason"])
	})

	t.Run("empty reason records unknown", func(t *testing.T) {
		t.Parallel()

		eventData := map[string]any{}
		addKillReason(eventData, "")

		assert.Equal(t, killReasonUnknown, eventData["kill_reason"])
	})
}

func TestRecordSandboxKill(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/server")
	counter, err := telemetry.GetCounter(meter, telemetry.OrchestratorSandboxKilledCounterName)
	require.NoError(t, err)

	recordSandboxKill(context.Background(), counter, "timeout")
	recordSandboxKill(context.Background(), counter, "")

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != string(telemetry.OrchestratorSandboxKilledCounterName) {
				continue
			}

			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)

			for _, dp := range sum.DataPoints {
				v, ok := dp.Attributes.Value(attribute.Key("kill_reason"))
				require.True(t, ok)
				got[v.AsString()] += dp.Value
			}
		}
	}

	assert.Equal(t, int64(1), got["timeout"])
	assert.Equal(t, int64(1), got[killReasonUnknown])
}
