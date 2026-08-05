package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type leaderStub struct{}

func (leaderStub) Observe(context.Context) (bool, error) { return true, nil }
func (leaderStub) Close(context.Context) error           { return nil }

type failingOverviewSource struct{}

func (failingOverviewSource) Fetch(context.Context) (Overview, error) {
	return Overview{}, errors.New("identity unavailable")
}

type countingFleetSource struct{ calls int }

func (s *countingFleetSource) Fetch(context.Context) (Fleet, error) {
	s.calls++

	return Fleet{}, nil
}

func TestRunnerHoldsAndInvalidatesMetricsWhenIdentitySourceFails(t *testing.T) {
	t.Parallel()
	metrics := &Metrics{}
	metrics.Record(Decision{DesiredWorkerHosts: 15})
	fleet := &countingFleetSource{}
	runner := Runner{
		Overview: failingOverviewSource{}, Fleet: fleet, Leadership: leaderStub{}, Engine: &Engine{}, Metrics: metrics,
		Logger: slog.New(slog.DiscardHandler), Interval: 10 * time.Second,
	}
	runner.reconcile(context.Background())
	if fleet.calls != 0 {
		t.Fatalf("fleet must not be evaluated after identity/capacity failure, got %d calls", fleet.calls)
	}
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), "GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, "monad_worker_autoscaler_snapshot_valid 0") || !strings.Contains(body, "monad_worker_autoscaler_failures_total 1") || strings.Contains(body, "monad_worker_autoscaler_desired_worker_hosts") {
		t.Fatalf("source failure did not produce a fail-closed hold:\n%s", body)
	}
}
