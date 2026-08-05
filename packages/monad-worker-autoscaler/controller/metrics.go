package controller

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

type Metrics struct {
	mu       sync.RWMutex
	leader   bool
	valid    bool
	decision Decision
	failures uint64
}

func (m *Metrics) SetLeader(leader bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leader = leader
}

func (m *Metrics) Record(decision Decision) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.valid = true
	m.decision = decision
}

func (m *Metrics) Fail() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.valid = false
	m.failures++
}

func (m *Metrics) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.valid = false
}

func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /metrics", m.writePrometheus)

	return mux
}

func (m *Metrics) writePrometheus(w http.ResponseWriter, _ *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	boolean := func(value bool) int {
		if value {
			return 1
		}

		return 0
	}
	_, _ = fmt.Fprintf(w, "# HELP monad_worker_autoscaler_leader Whether this allocation currently owns the Consul observation lock.\n# TYPE monad_worker_autoscaler_leader gauge\nmonad_worker_autoscaler_leader %d\n", boolean(m.leader))
	_, _ = fmt.Fprintf(w, "# HELP monad_worker_autoscaler_snapshot_valid Whether the most recent observation produced a valid decision.\n# TYPE monad_worker_autoscaler_snapshot_valid gauge\nmonad_worker_autoscaler_snapshot_valid %d\n", boolean(m.valid))
	_, _ = fmt.Fprintf(w, "# HELP monad_worker_autoscaler_failures_total Invalid or unavailable observation cycles.\n# TYPE monad_worker_autoscaler_failures_total counter\nmonad_worker_autoscaler_failures_total %d\n", m.failures)
	_, _ = fmt.Fprintf(w, "# HELP monad_worker_autoscaler_mutation_allowed Constant zero while the controller is shadow-only.\n# TYPE monad_worker_autoscaler_mutation_allowed gauge\nmonad_worker_autoscaler_mutation_allowed 0\n")
	if !m.valid {
		return
	}
	decision := m.decision
	_, _ = fmt.Fprintln(w, "# HELP monad_worker_autoscaler_direction Current shadow decision direction (exactly one label is 1).")
	_, _ = fmt.Fprintln(w, "# TYPE monad_worker_autoscaler_direction gauge")
	for _, direction := range []Direction{DirectionHold, DirectionScaleOut, DirectionScaleIn} {
		_, _ = fmt.Fprintf(w, "monad_worker_autoscaler_direction{direction=%q} %d\n", direction, boolean(decision.Direction == direction))
	}
	gauges := []struct {
		name  string
		help  string
		value float64
	}{
		{"snapshot_age_seconds", "Age of the accepted TAMS capacity snapshot.", decision.SnapshotAgeSeconds},
		{"durable_sessions", "Durable E2B sessions reported by TAMS.", float64(decision.DurableSessions)},
		{"active_workcells", "Active E2B workcells reported by TAMS.", float64(decision.ActiveWorkcells)},
		{"booting_workcells", "Booting E2B workcells reported by TAMS.", float64(decision.BootingWorkcells)},
		{"draining_workcells", "Draining E2B workcells reported by TAMS.", float64(decision.DrainingWorkcells)},
		{"parked_workcells", "Parked E2B workcells reported by TAMS.", float64(decision.ParkedWorkcells)},
		{"warm_target", "Configured warm workcell target reported by TAMS.", float64(decision.WarmTarget)},
		{"required_workcells", "Workcells counted by the beta capacity formula.", float64(decision.RequiredWorkcells)},
		{"desired_worker_hosts", "Shadow desired worker-host recommendation.", float64(decision.DesiredWorkerHosts)},
		{"actual_worker_hosts", "Ready worker hosts independently observed in Nomad.", float64(decision.ActualWorkerHosts)},
		{"draining_worker_hosts", "Ready but scheduling-ineligible worker hosts observed in Nomad.", float64(decision.DrainingWorkerHosts)},
		{"fixed_control_plane_nodes", "Fixed control-plane and build nodes reported separately from worker hosts.", float64(decision.FixedControlPlaneNodes)},
		{"low_demand_seconds", "Continuous low-demand evidence accumulated toward scale-in.", decision.LowDemandSeconds},
		{"scale_in_window_elapsed", "Whether the 15-minute low-demand window has elapsed.", float64(boolean(decision.ScaleInWindowElapsed))},
	}
	for _, gauge := range gauges {
		_, _ = fmt.Fprintf(w, "# HELP monad_worker_autoscaler_%s %s\n# TYPE monad_worker_autoscaler_%s gauge\nmonad_worker_autoscaler_%s %g\n", gauge.name, gauge.help, gauge.name, gauge.name, gauge.value)
	}
}
