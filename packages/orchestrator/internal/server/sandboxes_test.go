package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Test_server_List(t *testing.T) {
	type args struct {
		ctx context.Context
		in1 *emptypb.Empty
	}
	tests := []struct {
		name    string
		args    args
		want    *orchestrator.SandboxListResponse
		wantErr bool
		data    []*sandbox.Sandbox
	}{
		{
			name: "should return all sandboxes",

			args: args{
				ctx: context.Background(),
				in1: &emptypb.Empty{},
			},
			data: []*sandbox.Sandbox{
				{
					Config: &orchestrator.SandboxConfig{
						TemplateId: "template-id",
					},
				},
			},
			want: &orchestrator.SandboxListResponse{
				Sandboxes: []*orchestrator.RunningSandbox{
					{
						Config: &orchestrator.SandboxConfig{TemplateId: "template-id"},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				sandboxes: smap.New[*sandbox.Sandbox](),
				tracer:    noop.NewTracerProvider().Tracer(""),
			}
			for _, sbx := range tt.data {
				s.sandboxes.Insert(sbx.Config.SandboxId, sbx)
			}
			got, err := s.List(tt.args.ctx, tt.args.in1)
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
