package controller

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposeDecisionAndConstantMutationGuard(t *testing.T) {
	t.Parallel()
	metrics := &Metrics{}
	metrics.SetLeader(true)
	metrics.Record(Decision{
		DurableSessions:        100,
		ActiveWorkcells:        25,
		RequiredWorkcells:      27,
		DesiredWorkerHosts:     15,
		ActualWorkerHosts:      14,
		FixedControlPlaneNodes: FixedControlPlaneNodes,
		Direction:              DirectionScaleOut,
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), "GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, signal := range []string{
		"monad_worker_autoscaler_leader 1",
		"monad_worker_autoscaler_snapshot_valid 1",
		"monad_worker_autoscaler_mutation_allowed 0",
		"monad_worker_autoscaler_required_workcells 27",
		"monad_worker_autoscaler_desired_worker_hosts 15",
		"monad_worker_autoscaler_actual_worker_hosts 14",
		"monad_worker_autoscaler_fixed_control_plane_nodes 6",
		`monad_worker_autoscaler_direction{direction="scale_out"} 1`,
	} {
		if !strings.Contains(body, signal) {
			t.Errorf("metrics body does not contain %q:\n%s", signal, body)
		}
	}

	metrics.Clear()
	recorder = httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, request)
	if strings.Contains(recorder.Body.String(), "monad_worker_autoscaler_desired_worker_hosts") {
		t.Fatal("cleared follower metrics must not retain a stale recommendation")
	}
}
