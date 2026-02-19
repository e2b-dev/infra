package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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

			for _, sbx := range tt.data {
				sbx.SetStartedAt(startTime)
				sbx.SetEndAt(tt.endAt)
			}

			s := &Server{
				sandboxes: sandbox.NewSandboxesMap(),
				info:      &service.ServiceInfo{},
			}
			for _, sbx := range tt.data {
				s.sandboxes.Insert(sbx)
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
			Config: sandbox.Config{
				Vcpu:  2,
				RamMB: 512,
			},
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
	assert.IsType(t, float64(0), result["execution_time"])
	assert.Greater(t, result["execution_time"].(float64), float64(0))
}
