package sandbox

import (
	"context"
	"reflect"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
)

type fakeMetricStore struct {
	calls []string
}

func (l *fakeMetricStore) LogMetricsLoki(ctx context.Context) {
	l.calls = append(l.calls, "LogMetricsLoki")
}

func (l *fakeMetricStore) LogMetricsClickhouse(ctx context.Context) {
	l.calls = append(l.calls, "LogMetricsClickhouse")
}

func TestSandbox_logMetricsBasedOnConfig(t *testing.T) {
	type fields struct {
		ClickhouseStore      chdb.Store
		useLokiMetrics       string
		useClickhouseMetrics string
	}
	type args struct {
		ctx    context.Context
		logger *fakeMetricStore
		want   []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		// cover all the cases
		{
			name: "should call LogMetricsLoki if useLokiMetrics is true and useClickhouseMetrics is not set",
			fields: fields{
				useLokiMetrics: "true",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"LogMetricsLoki"},
			},
		},
		{
			name: "should call LogMetricsClickhouse if useClickhouseMetrics is true and useLokiMetrics is not set",
			fields: fields{
				useClickhouseMetrics: "true",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"LogMetricsClickhouse"},
			},
		},
		{
			name: "should call LogMetricsLoki neither are set",
			fields: fields{
				useLokiMetrics:       "",
				useClickhouseMetrics: "",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"LogMetricsLoki"},
			},
		},
		{
			name: "should call both if both are set",
			fields: fields{
				useLokiMetrics:       "true",
				useClickhouseMetrics: "true",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"LogMetricsLoki", "LogMetricsClickhouse"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Sandbox{
				ClickhouseStore:      tt.fields.ClickhouseStore,
				useLokiMetrics:       tt.fields.useLokiMetrics,
				useClickhouseMetrics: tt.fields.useClickhouseMetrics,
			}
			s.logMetricsBasedOnConfig(tt.args.ctx, tt.args.logger)
			if !reflect.DeepEqual(tt.args.logger.calls, tt.args.want) {
				t.Errorf("Sandbox.LogMetrics() = %v, want %v", tt.args.logger.calls, tt.args.want)
			}
		})
	}
}
