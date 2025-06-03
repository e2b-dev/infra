package handlers

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

// fake clickhouse store
type fakeClickhouseStore struct {
	*clickhouse.MockClient
	metrics []clickhouse.Metrics
	err     error
}

func (f *fakeClickhouseStore) QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]clickhouse.Metrics, error) {
	return f.metrics, f.err
}

var aTimestamp = time.Now().Add(-time.Hour * 24)

func TestAPIStore_getSandboxesSandboxIDMetricsClickhouse(t *testing.T) {
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
		// TODO: Add test cases.
		{
			name: "test",
			fields: fields{
				clickhouseStore: &fakeClickhouseStore{
					clickhouse.NewMockStore(),
					[]clickhouse.Metrics{
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
					clickhouse.NewMockStore(),
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
			a := &APIStore{

				clickhouseStore: tt.fields.clickhouseStore,
			}
			got, err := a.GetSandboxesSandboxIDMetricsFromClickhouse(tt.args.ctx, tt.args.sandboxID, tt.args.teamID, tt.args.limit, tt.args.duration)
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
type fakeMetricReader struct {
	legacyMetrics     []api.SandboxMetric
	legacyErr         error
	clickhouseMetrics []api.SandboxMetric
	clickhouseErr     error
}

func (f *fakeMetricReader) LegacyGetSandboxIDMetrics(ctx context.Context, sandboxID, teamID string, limit int, duration time.Duration) ([]api.SandboxMetric, error) {
	return f.legacyMetrics, f.legacyErr
}

func (f *fakeMetricReader) GetSandboxesSandboxIDMetricsFromClickhouse(ctx context.Context, sandboxID, teamID string, limit int, duration time.Duration) ([]api.SandboxMetric, error) {
	return f.clickhouseMetrics, f.clickhouseErr
}

func TestAPIStore_readMetricsBasedOnConfig(t *testing.T) {
	type fields struct {
		readMetricsFromClickHouse string
	}
	type args struct {
		ctx       context.Context
		sandboxID string
		teamID    string
		reader    metricReader
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []api.SandboxMetric
		wantErr bool
	}{
		// todo test all combinations of legacy and clickhouse metrics
		{
			name: "test legacy metrics",
			fields: fields{
				readMetricsFromClickHouse: "",
			},
			args: args{
				reader: &fakeMetricReader{
					legacyMetrics: []api.SandboxMetric{
						{
							Timestamp: aTimestamp,
						},
					},
				},
			},
			want: []api.SandboxMetric{
				{
					Timestamp: aTimestamp,
				},
			},
			wantErr: false,
		},
		{
			name: "test clickhouse metrics",
			fields: fields{
				readMetricsFromClickHouse: "true",
			},
			args: args{
				reader: &fakeMetricReader{
					clickhouseMetrics: []api.SandboxMetric{
						{
							Timestamp: aTimestamp,
						},
					},
				},
			},
			want: []api.SandboxMetric{
				{
					Timestamp: aTimestamp,
				},
			},
			wantErr: false,
		},
		{
			name: "test random string",
			fields: fields{
				readMetricsFromClickHouse: "random string",
			},
			args: args{
				reader: &fakeMetricReader{
					legacyMetrics: []api.SandboxMetric{
						{
							Timestamp: aTimestamp,
						},
					},
				},
			},
			want: []api.SandboxMetric{
				{
					Timestamp: aTimestamp,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &APIStore{
				readMetricsFromClickHouse: tt.fields.readMetricsFromClickHouse,
			}
			got, err := a.readMetricsBasedOnConfig(tt.args.ctx, tt.args.sandboxID, tt.args.teamID, tt.args.reader)
			if (err != nil) != tt.wantErr {
				t.Errorf("APIStore.readMetricsBasedOnConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("APIStore.readMetricsBasedOnConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}
