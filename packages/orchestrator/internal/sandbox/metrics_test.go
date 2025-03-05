package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/chmodels"
)

// fake clickhouse store
type fakeClickhouseStore struct {
	*chdb.MockStore
	err error
}

func (f *fakeClickhouseStore) InsertMetrics(ctx context.Context, metrics chmodels.Metrics) error {
	return f.err
}

type fakeSandbox struct {
	*Sandbox
	metrics SandboxMetrics
	err     error
}

func (f *fakeSandbox) GetMetrics(ctx context.Context) (SandboxMetrics, error) {
	return f.metrics, f.err
}

var aTimestamp = time.Now().Add(-time.Hour * 24)

func TestSandbox_SendMetrics(t *testing.T) {
	type fields struct {
		ClickhouseStore chdb.Store
	}
	type sandboxFields struct {
		Config          *orchestrator.SandboxConfig
		ClickhouseStore chdb.Store
	}
	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name          string
		fields        fields
		sandboxFields sandboxFields
		args          args
	}{
		{
			name: "test",
			fields: fields{
				ClickhouseStore: &fakeClickhouseStore{},
			},
			sandboxFields: sandboxFields{
				Config: &orchestrator.SandboxConfig{
					EnvdVersion: minEnvdVersionForMetrcis,
				},
			},
			args: args{
				ctx: context.Background(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeSandbox{
				Sandbox: &Sandbox{
					Config:          tt.sandboxFields.Config,
					ClickhouseStore: tt.fields.ClickhouseStore,
				},
				metrics: SandboxMetrics{
					Timestamp:      aTimestamp.Unix(),
					CPUCount:       1,
					CPUUsedPercent: 100,
					MemTotalMiB:    1024,
					MemUsedMiB:     512,
				},
				err: nil,
			}
			s.SendMetrics(tt.args.ctx)
		})
	}
}
