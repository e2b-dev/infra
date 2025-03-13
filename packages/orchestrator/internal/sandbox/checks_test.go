package sandbox

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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
			s.LogMetricsBasedOnConfig(tt.args.ctx, tt.args.logger)
			if !reflect.DeepEqual(tt.args.logger.calls, tt.args.want) {
				t.Errorf("Sandbox.logMetricsBasedOnConfig() = %v, want %v", tt.args.logger.calls, tt.args.want)
			}
		})
	}
}

type fakeLogHealthAndUsage struct {
	calls []string
}

func (l *fakeLogHealthAndUsage) LogMetricsBasedOnConfig(ctx context.Context, logger metricStore) {
	l.calls = append(l.calls, "LogMetricsBasedOnConfig")
}

func (l *fakeLogHealthAndUsage) Healthcheck(ctx context.Context, alwaysReport bool) {
	l.calls = append(l.calls, "Healthcheck")
}

func TestSandbox_logHeathAndUsage(t *testing.T) {

	type args struct {
		ctx        *utils.LockableCancelableContext
		state      fakeLogHealthAndUsage
		duration   time.Duration
		assertions []func(t *testing.T, s fakeLogHealthAndUsage)
	}
	tests := []struct {
		name       string
		args       args
		assertions []func(t *testing.T, s fakeLogHealthAndUsage)
	}{
		// TODO: Add test cases.
		{
			name: "should call LogMetricsBasedOnConfig and Healthcheck",
			args: args{
				ctx:      utils.NewLockableCancelableContext(context.Background()),
				state:    fakeLogHealthAndUsage{},
				duration: time.Duration(metricsCheckInterval) + time.Second, // +1 second to ensure the context is cancelled after logging metrics is done
			},
			assertions: []func(t *testing.T, s fakeLogHealthAndUsage){
				func(t *testing.T, s fakeLogHealthAndUsage) {
					// assert healthcheck is called 2 times
					count := 0
					for _, call := range s.calls {
						if call == "LogMetricsBasedOnConfig" {
							count++
						}
					}
					if count != 2 {
						t.Errorf("expected 2 calls, got %d", count)
					}
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Sandbox{}
			go s.logHeathAndUsage(tt.args.ctx, &tt.args.state)
			time.Sleep(tt.args.duration)
			tt.args.ctx.Cancel()
			for _, assertion := range tt.assertions {
				assertion(t, tt.args.state)
			}
		})
	}
}
