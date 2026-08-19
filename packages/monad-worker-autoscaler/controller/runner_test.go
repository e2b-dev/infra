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

type stubOverviewSource struct{ overview Overview }

func (s stubOverviewSource) Fetch(context.Context) (Overview, error) { return s.overview, nil }

type stubFleetSource struct{ fleet Fleet }

func (s stubFleetSource) Fetch(context.Context) (Fleet, error) { return s.fleet, nil }

type followerStub struct{}

func (followerStub) Observe(context.Context) (bool, error) { return false, nil }
func (followerStub) Close(context.Context) error           { return nil }

type recordingMutator struct {
	decisions []Decision
	err       error
}

func (m *recordingMutator) Apply(_ context.Context, decision Decision) error {
	m.decisions = append(m.decisions, decision)

	return m.err
}

func scaleOutOverview() Overview {
	overview := validOverview(time.Now())
	overview.Capacity.ActiveWorkcells = 6
	overview.Capacity.DurableSessions = 6
	overview.Capacity.CountedWorkcells = 6
	overview.Capacity.DesiredWorkerHosts = 4

	return overview
}

func TestRunnerAppliesMutatorOnLeaderScaleOutDecision(t *testing.T) {
	t.Parallel()
	mutator := &recordingMutator{}
	runner := Runner{
		Overview: stubOverviewSource{overview: scaleOutOverview()}, Fleet: stubFleetSource{fleet: Fleet{ActualHosts: 2}},
		Leadership: leaderStub{}, Engine: &Engine{ScaleOutMutationEnabled: true}, Metrics: &Metrics{},
		Logger: slog.New(slog.DiscardHandler), Interval: 10 * time.Second, Mutator: mutator,
	}
	runner.reconcile(context.Background())
	if len(mutator.decisions) != 1 || mutator.decisions[0].Direction != DirectionScaleOut || !mutator.decisions[0].MutationAllowed {
		t.Fatalf("expected one actuatable scale-out decision, got %+v", mutator.decisions)
	}
}

func TestRunnerMutatorFailureDoesNotBreakObservation(t *testing.T) {
	t.Parallel()
	mutator := &recordingMutator{err: errors.New("resize refused")}
	metrics := &Metrics{}
	runner := Runner{
		Overview: stubOverviewSource{overview: scaleOutOverview()}, Fleet: stubFleetSource{fleet: Fleet{ActualHosts: 2}},
		Leadership: leaderStub{}, Engine: &Engine{ScaleOutMutationEnabled: true}, Metrics: metrics,
		Logger: slog.New(slog.DiscardHandler), Interval: 10 * time.Second, Mutator: mutator,
	}
	runner.reconcile(context.Background())
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), "GET", "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), "monad_worker_autoscaler_snapshot_valid 1") {
		t.Fatalf("a mutation failure must not invalidate the accepted observation:\n%s", recorder.Body.String())
	}
}

func TestRunnerNeverMutatesAsFollowerOrOnFailedObservation(t *testing.T) {
	t.Parallel()
	follower := &recordingMutator{}
	runner := Runner{
		Overview: stubOverviewSource{overview: scaleOutOverview()}, Fleet: stubFleetSource{fleet: Fleet{ActualHosts: 2}},
		Leadership: followerStub{}, Engine: &Engine{ScaleOutMutationEnabled: true}, Metrics: &Metrics{},
		Logger: slog.New(slog.DiscardHandler), Interval: 10 * time.Second, Mutator: follower,
	}
	runner.reconcile(context.Background())
	if len(follower.decisions) != 0 {
		t.Fatalf("follower must never mutate, got %+v", follower.decisions)
	}

	failed := &recordingMutator{}
	runner = Runner{
		Overview: failingOverviewSource{}, Fleet: stubFleetSource{fleet: Fleet{ActualHosts: 2}},
		Leadership: leaderStub{}, Engine: &Engine{ScaleOutMutationEnabled: true}, Metrics: &Metrics{},
		Logger: slog.New(slog.DiscardHandler), Interval: 10 * time.Second, Mutator: failed,
	}
	runner.reconcile(context.Background())
	if len(failed.decisions) != 0 {
		t.Fatalf("failed observation must never mutate, got %+v", failed.decisions)
	}
}
