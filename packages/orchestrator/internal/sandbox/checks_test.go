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

func (l *fakeMetricStore) LogMetrics(ctx context.Context) {
	l.calls = append(l.calls, "LogMetrics")
}

func (l *fakeMetricStore) SendMetrics(ctx context.Context) {
	l.calls = append(l.calls, "SendMetrics")
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
			name: "should call LogMetrics if useLokiMetrics is true and useClickhouseMetrics is not set",
			fields: fields{
				useLokiMetrics: "true",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"LogMetrics"},
			},
		},
		{
			name: "should call SendMetrics if useClickhouseMetrics is true and useLokiMetrics is not set",
			fields: fields{
				useClickhouseMetrics: "true",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"SendMetrics"},
			},
		},
		{
			name: "should call LogMetrics neither are set",
			fields: fields{
				useLokiMetrics:       "",
				useClickhouseMetrics: "",
			},
			args: args{
				logger: &fakeMetricStore{},
				want:   []string{"LogMetrics"},
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
				want:   []string{"LogMetrics", "SendMetrics"},
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
				t.Errorf("Sandbox.logMetricsBasedOnConfig() = %v, want %v", tt.args.logger.calls, tt.args.want)
			}
		})
	}
}
