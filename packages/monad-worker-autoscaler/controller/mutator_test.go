package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeActuator struct {
	target      int
	targetErr   error
	resizeErr   error
	resizeCalls []int
}

func (f *fakeActuator) TargetSize(context.Context) (int, error) {
	if f.targetErr != nil {
		return 0, f.targetErr
	}

	return f.target, nil
}

func (f *fakeActuator) Resize(_ context.Context, target int) error {
	f.resizeCalls = append(f.resizeCalls, target)

	return f.resizeErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func scaleOutDecision(desired int) Decision {
	return Decision{Direction: DirectionScaleOut, MutationAllowed: true, DesiredWorkerHosts: desired}
}

func TestNewScaleOutMutatorValidation(t *testing.T) {
	actuator := &fakeActuator{}
	metrics := &Metrics{}
	logger := discardLogger()
	for _, floor := range []int{0, 1, 16, -3} {
		if _, err := NewScaleOutMutator(actuator, floor, metrics, logger); err == nil {
			t.Fatalf("expected floor rejection for %d", floor)
		}
	}
	if _, err := NewScaleOutMutator(nil, 4, metrics, logger); err == nil {
		t.Fatal("expected nil actuator rejection")
	}
	if _, err := NewScaleOutMutator(actuator, 4, nil, logger); err == nil {
		t.Fatal("expected nil metrics rejection")
	}
	if _, err := NewScaleOutMutator(actuator, 4, metrics, nil); err == nil {
		t.Fatal("expected nil logger rejection")
	}
	if _, err := NewScaleOutMutator(actuator, 4, metrics, logger); err != nil {
		t.Fatalf("construct mutator: %v", err)
	}
}

func TestScaleOutMutatorNeverActuatesNonScaleOut(t *testing.T) {
	actuator := &fakeActuator{target: 4}
	metrics := &Metrics{}
	mutator, err := NewScaleOutMutator(actuator, 4, metrics, discardLogger())
	if err != nil {
		t.Fatalf("construct mutator: %v", err)
	}
	decisions := []Decision{
		{Direction: DirectionHold, MutationAllowed: false, DesiredWorkerHosts: 4},
		{Direction: DirectionScaleIn, MutationAllowed: false, DesiredWorkerHosts: 3},
		// A scale-in decision can never carry mutation permission, but even a
		// corrupted one must not reach the actuator.
		{Direction: DirectionScaleIn, MutationAllowed: true, DesiredWorkerHosts: 3},
		{Direction: DirectionScaleOut, MutationAllowed: false, DesiredWorkerHosts: 6},
	}
	for _, decision := range decisions {
		if err := mutator.Apply(context.Background(), decision); err != nil {
			t.Fatalf("apply %s: %v", decision.Direction, err)
		}
	}
	if len(actuator.resizeCalls) != 0 {
		t.Fatalf("expected zero actuator calls, got %v", actuator.resizeCalls)
	}
}

func TestScaleOutMutatorResizesTowardDesired(t *testing.T) {
	actuator := &fakeActuator{target: 4}
	metrics := &Metrics{}
	mutator, err := NewScaleOutMutator(actuator, 4, metrics, discardLogger())
	if err != nil {
		t.Fatalf("construct mutator: %v", err)
	}
	if err := mutator.Apply(context.Background(), scaleOutDecision(6)); err != nil {
		t.Fatalf("apply scale-out: %v", err)
	}
	if len(actuator.resizeCalls) != 1 || actuator.resizeCalls[0] != 6 {
		t.Fatalf("expected one resize to 6, got %v", actuator.resizeCalls)
	}
}

func TestScaleOutMutatorFloorAndIdempotency(t *testing.T) {
	cases := []struct {
		name    string
		desired int
		current int
		want    []int
	}{
		{"desired below floor holds at floor", 3, 4, nil},
		{"already at target", 6, 6, nil},
		{"never shrink", 6, 8, nil},
		{"resize in flight", 6, 6, nil},
		{"floor lifts target", 3, 2, []int{4}},
		{"grow from floor", 5, 4, []int{5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actuator := &fakeActuator{target: tc.current}
			mutator, err := NewScaleOutMutator(actuator, 4, &Metrics{}, discardLogger())
			if err != nil {
				t.Fatalf("construct mutator: %v", err)
			}
			if err := mutator.Apply(context.Background(), scaleOutDecision(tc.desired)); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(actuator.resizeCalls) != len(tc.want) {
				t.Fatalf("expected %v resizes, got %v", tc.want, actuator.resizeCalls)
			}
			for i := range tc.want {
				if actuator.resizeCalls[i] != tc.want[i] {
					t.Fatalf("expected %v resizes, got %v", tc.want, actuator.resizeCalls)
				}
			}
		})
	}
}

func TestScaleOutMutatorRefusesAmbiguousOrOutOfEnvelope(t *testing.T) {
	t.Run("ceiling exceeded", func(t *testing.T) {
		actuator := &fakeActuator{target: 4}
		mutator, _ := NewScaleOutMutator(actuator, 4, &Metrics{}, discardLogger())
		if err := mutator.Apply(context.Background(), scaleOutDecision(MaximumWorkerHosts+1)); err == nil {
			t.Fatal("expected ceiling rejection")
		}
		if len(actuator.resizeCalls) != 0 {
			t.Fatalf("expected zero resizes, got %v", actuator.resizeCalls)
		}
	})
	t.Run("ambiguous observed target", func(t *testing.T) {
		actuator := &fakeActuator{target: MaximumWorkerHosts + 2}
		mutator, _ := NewScaleOutMutator(actuator, 4, &Metrics{}, discardLogger())
		if err := mutator.Apply(context.Background(), scaleOutDecision(6)); err == nil {
			t.Fatal("expected ambiguous-state rejection")
		}
		if len(actuator.resizeCalls) != 0 {
			t.Fatalf("expected zero resizes, got %v", actuator.resizeCalls)
		}
	})
	t.Run("target read failure", func(t *testing.T) {
		actuator := &fakeActuator{targetErr: errors.New("boom")}
		mutator, _ := NewScaleOutMutator(actuator, 4, &Metrics{}, discardLogger())
		if err := mutator.Apply(context.Background(), scaleOutDecision(6)); err == nil {
			t.Fatal("expected read-failure rejection")
		}
		if len(actuator.resizeCalls) != 0 {
			t.Fatalf("expected zero resizes, got %v", actuator.resizeCalls)
		}
	})
	t.Run("resize failure propagates", func(t *testing.T) {
		actuator := &fakeActuator{target: 4, resizeErr: errors.New("boom")}
		metrics := &Metrics{}
		mutator, _ := NewScaleOutMutator(actuator, 4, metrics, discardLogger())
		if err := mutator.Apply(context.Background(), scaleOutDecision(6)); err == nil {
			t.Fatal("expected resize-failure rejection")
		}
	})
}

func metricsBody(t *testing.T, metrics *Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)

	return recorder.Body.String()
}

func TestMetricsMutationSignals(t *testing.T) {
	metrics := &Metrics{}
	body := metricsBody(t, metrics)
	if !strings.Contains(body, "monad_worker_autoscaler_mutation_allowed 0") {
		t.Fatalf("expected mutation_allowed 0 before any decision, got:\n%s", body)
	}
	if !strings.Contains(body, "monad_worker_autoscaler_resizes_total 0") {
		t.Fatalf("expected resizes_total 0, got:\n%s", body)
	}

	metrics.Record(scaleOutDecision(6))
	body = metricsBody(t, metrics)
	if !strings.Contains(body, "monad_worker_autoscaler_mutation_allowed 1") {
		t.Fatalf("expected mutation_allowed 1 for an allowed scale-out decision, got:\n%s", body)
	}

	metrics.SetMIGTarget(4)
	metrics.RecordResize(6)
	body = metricsBody(t, metrics)
	for _, line := range []string{
		"monad_worker_autoscaler_resizes_total 1",
		"monad_worker_autoscaler_last_resize_target 6",
		"monad_worker_autoscaler_mig_target_hosts 4",
	} {
		if !strings.Contains(body, line) {
			t.Fatalf("expected %q, got:\n%s", line, body)
		}
	}

	// A follower must clear stale gauges but never the monotonic counter.
	metrics.Clear()
	body = metricsBody(t, metrics)
	if strings.Contains(body, "monad_worker_autoscaler_mig_target_hosts") {
		t.Fatalf("expected mig_target_hosts cleared, got:\n%s", body)
	}
	if strings.Contains(body, "monad_worker_autoscaler_last_resize_target") {
		t.Fatalf("expected last_resize_target cleared, got:\n%s", body)
	}
	if !strings.Contains(body, "monad_worker_autoscaler_resizes_total 1") {
		t.Fatalf("expected resizes_total to survive Clear, got:\n%s", body)
	}
	if !strings.Contains(body, "monad_worker_autoscaler_mutation_allowed 0") {
		t.Fatalf("expected mutation_allowed 0 after Clear, got:\n%s", body)
	}
}
