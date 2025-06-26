package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

// fake clickhouse store
type fakeClickhouseStore struct {
	clickhouse.Clickhouse
	metrics []clickhouse.Metrics
	err     error
}

func (f *fakeClickhouseStore) QueryLatestMetrics(ctx context.Context, sandboxID, teamID string) ([]clickhouse.Metrics, error) {
	return f.metrics, f.err
}

var aTimestamp = time.Now().Add(-time.Hour * 24)

func TestAPIStore_getSandboxesSandboxIDMetrics(t *testing.T) {
	type fields struct {
		clickhouseStore clickhouse.Clickhouse
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
		{
			name: "test",
			fields: fields{
				clickhouseStore: &fakeClickhouseStore{
					clickhouse.NewNoopClient(),
					[]clickhouse.Metrics{
						{
							SandboxID:      "sandbox1",
							TeamID:         "team1",
							CPUUsedPercent: 10,
							MemUsed:        100,
							MemTotal:       100,
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
					clickhouse.NewNoopClient(),
					[]clickhouse.Metrics{},
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
			got, err := getSandboxesSandboxIDMetrics(tt.args.ctx, tt.fields.clickhouseStore, []string{tt.args.sandboxID}, tt.args.teamID, tt.args.limit, tt.args.duration)
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

// fake metric reader
