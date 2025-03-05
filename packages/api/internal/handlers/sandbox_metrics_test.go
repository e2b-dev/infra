package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/chmodels"
	"github.com/google/go-cmp/cmp"
)

// fake clickhouse store
type fakeClickhouseStore struct {
	*chdb.MockStore
	metrics []chmodels.Metrics
	err     error
}

func (f *fakeClickhouseStore) QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]chmodels.Metrics, error) {
	return f.metrics, f.err
}

var aTimestamp = time.Now().Add(-time.Hour * 24)

func TestAPIStore_getSandboxesSandboxIDMetrics(t *testing.T) {
	type fields struct {
		clickhouseStore chdb.Store
	}
	type args struct {
		ctx       context.Context
		sandboxID string
		teamID    string
		limit     int
		duration  time.Duration
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []api.SandboxMetric
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "test",
			fields: fields{
				clickhouseStore: &fakeClickhouseStore{
					chdb.NewMockStore(),
					[]chmodels.Metrics{
						{
							SandboxID:      "sandbox1",
							TeamID:         "team1",
							CPUUsedPercent: 10,
							MemUsedMiB:     100,
							MemTotalMiB:    100,
							CPUCount:       1,
							Timestamp:      aTimestamp,
						},
					},
					nil,
				},
			},
			args: args{
				ctx:       context.Background(),
				sandboxID: "sandbox1",
				teamID:    "team1",
				limit:     10,
				duration:  time.Hour * 24,
			},
			want: []api.SandboxMetric{
				{
					CpuCount:    1,
					CpuUsedPct:  10,
					MemTotalMiB: 100,
					MemUsedMiB:  100,
					Timestamp:   aTimestamp,
				},
			},
		},
		{
			name: "test error",
			fields: fields{
				clickhouseStore: &fakeClickhouseStore{
					chdb.NewMockStore(),
					[]chmodels.Metrics{},
					errors.New("test error"),
				},
			},
			args: args{
				ctx:       context.Background(),
				sandboxID: "sandbox1",
				teamID:    "team1",
				limit:     10,
				duration:  time.Hour * 24,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &APIStore{

				clickhouseStore: tt.fields.clickhouseStore,
			}
			got, err := a.getSandboxesSandboxIDMetrics(tt.args.ctx, tt.args.sandboxID, tt.args.teamID, tt.args.limit, tt.args.duration)
			if (err != nil) != tt.wantErr {
				t.Errorf("APIStore.getSandboxesSandboxIDMetrics() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if out := cmp.Diff(got, tt.want); out != "" {
				t.Errorf("APIStore.getSandboxesSandboxIDMetrics() = %v, want %v", got, tt.want)
			}
		})
	}
}
