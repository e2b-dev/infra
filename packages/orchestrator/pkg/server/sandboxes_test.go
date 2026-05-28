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
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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
