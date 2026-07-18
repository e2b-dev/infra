package target

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/hashicorp/nomad/api"
)

const testNamespace = "workloads"

type nomadStub struct {
	t                       *testing.T
	mu                      sync.Mutex
	count                   int
	jobModifyIndex          uint64
	autoRevert              *bool
	deployment              *api.Deployment
	scaleConflicts          int
	injectDeploymentOnScale bool
	scaleDeployments        int
	calls                   []string
	scaleRequests           []api.ScalingRequest
	status                  api.JobScaleStatusResponse
}

func newNomadStub(t *testing.T) *nomadStub {
	t.Helper()

	return &nomadStub{
		t:              t,
		count:          2,
		jobModifyIndex: 20,
		status: api.JobScaleStatusResponse{
			JobID:      "example",
			Namespace:  testNamespace,
			JobStopped: true,
			TaskGroups: map[string]api.TaskGroupScaleStatus{
				"web": {Desired: 3, Running: 2, Events: []api.ScalingEvent{{Time: 12345}}},
			},
		},
	}
}

func (s *nomadStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if got := r.URL.Query().Get("namespace"); got != testNamespace {
		http.Error(w, "wrong namespace "+got, http.StatusBadRequest)

		return
	}
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/job/example":
		writeJSON(s.t, w, s.job())
	case r.Method == http.MethodGet && r.URL.Path == "/v1/job/example/deployment":
		writeJSON(s.t, w, s.deployment)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/deployment/fail/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/deployment/fail/")
		if s.deployment != nil && s.deployment.ID == id {
			s.deployment.Status = api.DeploymentStatusFailed
			s.deployment.ModifyIndex++
		}
		writeJSON(s.t, w, map[string]any{})
	case r.Method == http.MethodPut && r.URL.Path == "/v1/job/example/scale":
		var req api.ScalingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.t.Errorf("decode scale request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		s.scaleRequests = append(s.scaleRequests, req)
		if s.injectDeploymentOnScale {
			// Simulate a deployment appearing between the plugin's read and
			// its scale write.
			s.injectDeploymentOnScale = false
			s.deployment = &api.Deployment{
				ID:             "deployment-injected",
				JobID:          "example",
				Namespace:      testNamespace,
				JobCreateIndex: 10,
				Status:         api.DeploymentStatusRunning,
				CreateIndex:    40,
				ModifyIndex:    41,
			}
			http.Error(w, "job scaling blocked due to active deployment", http.StatusBadRequest)

			return
		}
		if deploymentActive(s.deployment) {
			// Real Nomad rejects scaling while a deployment is in progress.
			http.Error(w, "job scaling blocked due to active deployment", http.StatusBadRequest)

			return
		}
		if s.scaleConflicts > 0 {
			s.scaleConflicts--
			s.jobModifyIndex++
			http.Error(w, "job modify index conflict", http.StatusConflict)

			return
		}
		if req.Count != nil {
			s.count = int(*req.Count)
		}
		s.jobModifyIndex++
		// Real Nomad registers a new job version and spawns a deployment to
		// roll out the new count (for jobs with an update stanza).
		s.scaleDeployments++
		s.deployment = &api.Deployment{
			ID:             fmt.Sprintf("deployment-scale-%d", s.scaleDeployments),
			JobID:          "example",
			Namespace:      testNamespace,
			JobCreateIndex: 10,
			Status:         api.DeploymentStatusRunning,
			CreateIndex:    50 + uint64(s.scaleDeployments),
			ModifyIndex:    50 + uint64(s.scaleDeployments),
		}
		writeJSON(s.t, w, map[string]any{})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/job/example/scale":
		writeJSON(s.t, w, s.status)
	default:
		http.Error(w, "unexpected request", http.StatusNotFound)
	}
}

func (s *nomadStub) job() *api.Job {
	taskGroup := api.NewTaskGroup("web", s.count)
	if s.autoRevert != nil {
		taskGroup.Update = &api.UpdateStrategy{AutoRevert: s.autoRevert}
	}

	return &api.Job{
		ID:             new("example"),
		Namespace:      new(testNamespace),
		CreateIndex:    new(uint64(10)),
		JobModifyIndex: new(s.jobModifyIndex),
		TaskGroups:     []*api.TaskGroup{taskGroup},
	}
}

func (s *nomadStub) snapshot() ([]string, []api.ScalingRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.calls...), append([]api.ScalingRequest(nil), s.scaleRequests...)
}

func (s *nomadStub) deploymentSnapshot() *api.Deployment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deployment == nil {
		return nil
	}
	copied := *s.deployment

	return &copied
}

func deploymentActive(deployment *api.Deployment) bool {
	if deployment == nil {
		return false
	}
	switch deployment.Status {
	case api.DeploymentStatusRunning, api.DeploymentStatusPaused, api.DeploymentStatusBlocked,
		api.DeploymentStatusUnblocking, api.DeploymentStatusPending, deploymentStatusInitializing:
		return true
	default:
		return false
	}
}

func newTestPlugin(t *testing.T, stub *nomadStub) *Plugin {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)

	p := New(hclog.NewNullLogger())
	p.retryDelay = 0
	if err := p.SetConfig(map[string]string{
		"nomad_address":   server.URL,
		"nomad_namespace": "configured-default",
	}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	return p
}

func targetConfig() map[string]string {
	return map[string]string{
		sdk.TargetConfigKeyJob:       "example",
		sdk.TargetConfigKeyTaskGroup: "web",
		sdk.TargetConfigKeyNamespace: testNamespace,
	}
}

func TestScaleNormal(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	p := newTestPlugin(t, stub)
	action := sdk.ScalingAction{
		Count:  5,
		Reason: "load increased",
		Error:  true,
		Meta:   map[string]any{"policy": "web"},
	}

	if err := p.Scale(action, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}

	calls, requests := stub.snapshot()
	wantCalls := []string{
		"GET /v1/job/example",
		"GET /v1/job/example/deployment",
		"PUT /v1/job/example/scale",
		"GET /v1/job/example",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if len(requests) != 1 {
		t.Fatalf("scale request count = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Count == nil || *req.Count != 5 || req.JobModifyIndex != 20 {
		t.Fatalf("scale request count/index = %v/%d", req.Count, req.JobModifyIndex)
	}
	if req.Message != action.Reason || req.Error != action.Error || !reflect.DeepEqual(req.Meta, action.Meta) {
		t.Fatalf("scale action fields not preserved: %#v", req)
	}
	rollout := stub.deploymentSnapshot()
	if rollout == nil || rollout.Status != api.DeploymentStatusRunning {
		t.Fatalf("rollout deployment spawned by the scale was not left running: %#v", rollout)
	}
}

func TestScaleFailsActiveDeploymentBeforeScaling(t *testing.T) {
	t.Parallel()

	statuses := []string{
		api.DeploymentStatusRunning,
		api.DeploymentStatusPaused,
		api.DeploymentStatusBlocked,
		api.DeploymentStatusUnblocking,
		api.DeploymentStatusPending,
		deploymentStatusInitializing,
	}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			stub := newNomadStub(t)
			stub.deployment = &api.Deployment{
				ID:             "deployment-1",
				JobID:          "example",
				Namespace:      testNamespace,
				JobCreateIndex: 10,
				Status:         status,
				CreateIndex:    30,
				ModifyIndex:    31,
			}
			p := newTestPlugin(t, stub)

			if err := p.Scale(sdk.ScalingAction{Count: 4}, targetConfig()); err != nil {
				t.Fatalf("Scale: %v", err)
			}

			calls, _ := stub.snapshot()
			wantCalls := []string{
				"GET /v1/job/example",
				"GET /v1/job/example/deployment",
				"PUT /v1/deployment/fail/deployment-1",
				"GET /v1/job/example",
				"GET /v1/job/example/deployment",
				"PUT /v1/job/example/scale",
				"GET /v1/job/example",
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
			}
			rollout := stub.deploymentSnapshot()
			if rollout == nil || rollout.ID == "deployment-1" || rollout.Status != api.DeploymentStatusRunning {
				t.Fatalf("rollout deployment spawned by the scale was not left running: %#v", rollout)
			}
		})
	}
}

func TestScaleLeavesOwnRolloutAloneWhenCountMatches(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.count = 4
	stub.deployment = &api.Deployment{
		ID:             "deployment-1",
		JobID:          "example",
		Namespace:      testNamespace,
		JobCreateIndex: 10,
		Status:         api.DeploymentStatusRunning,
		CreateIndex:    30,
		ModifyIndex:    31,
	}
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: 4}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}

	calls, requests := stub.snapshot()
	wantCalls := []string{"GET /v1/job/example"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if len(requests) != 0 {
		t.Fatalf("scale requests = %d, want 0", len(requests))
	}
	rollout := stub.deploymentSnapshot()
	if rollout.Status != api.DeploymentStatusRunning {
		t.Fatalf("in-flight rollout was disturbed: %#v", rollout)
	}
}

func TestScaleRetriesWhenDeploymentAppearsBeforeWrite(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.injectDeploymentOnScale = true
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: 6}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}

	calls, requests := stub.snapshot()
	if len(requests) != 2 {
		t.Fatalf("scale request count = %d, want 2", len(requests))
	}
	failedInjected := false
	for _, call := range calls {
		if call == "PUT /v1/deployment/fail/deployment-injected" {
			failedInjected = true
		}
	}
	if !failedInjected {
		t.Fatalf("conflicting deployment was not failed before retrying: %#v", calls)
	}
	rollout := stub.deploymentSnapshot()
	if rollout == nil || rollout.ID == "deployment-injected" || rollout.Status != api.DeploymentStatusRunning {
		t.Fatalf("rollout deployment spawned by the retried scale was not left running: %#v", rollout)
	}
}

func TestScaleRefusesAutoRevertJob(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.autoRevert = new(true)
	stub.deployment = &api.Deployment{
		ID:             "deployment-1",
		JobID:          "example",
		Namespace:      testNamespace,
		JobCreateIndex: 10,
		Status:         api.DeploymentStatusRunning,
		CreateIndex:    30,
		ModifyIndex:    31,
	}
	p := newTestPlugin(t, stub)

	err := p.Scale(sdk.ScalingAction{Count: 4}, targetConfig())
	if err == nil || !strings.Contains(err.Error(), "auto_revert") {
		t.Fatalf("Scale error = %v, want auto_revert configuration error", err)
	}

	calls, requests := stub.snapshot()
	// The guard trips before any deployment read or mutation, and the error
	// is terminal so there is no retry.
	wantCalls := []string{"GET /v1/job/example"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if len(requests) != 0 {
		t.Fatalf("scale requests = %d, want 0", len(requests))
	}
	rollout := stub.deploymentSnapshot()
	if rollout.Status != api.DeploymentStatusRunning {
		t.Fatalf("deployment of auto_revert job was disturbed: %#v", rollout)
	}
}

func TestScaleAutoRevertJobNoOpWhenCountMatches(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.autoRevert = new(true)
	stub.count = 4
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: 4}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}
}

func TestScaleAllowsExplicitAutoRevertFalse(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.autoRevert = new(false)
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: 5}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}
}

func TestScaleLeavesTerminalDeploymentAlone(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.deployment = &api.Deployment{
		ID:             "deployment-1",
		JobCreateIndex: 10,
		Status:         api.DeploymentStatusSuccessful,
	}
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: 3}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}

	calls, _ := stub.snapshot()
	for _, call := range calls {
		if strings.HasPrefix(call, "PUT /v1/deployment/fail/") {
			t.Fatal("terminal deployment was failed")
		}
	}
}

func TestScaleDryRunDoesNotReadOrWrite(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.deployment = &api.Deployment{
		ID:             "deployment-1",
		JobCreateIndex: 10,
		Status:         api.DeploymentStatusRunning,
	}
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: sdk.StrategyActionMetaValueDryRunCount}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}
	if calls, _ := stub.snapshot(); len(calls) != 0 {
		t.Fatalf("dry run made requests: %#v", calls)
	}
}

func TestScaleRetriesCASRaceFromFreshJob(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	stub.scaleConflicts = 1
	p := newTestPlugin(t, stub)

	if err := p.Scale(sdk.ScalingAction{Count: 6}, targetConfig()); err != nil {
		t.Fatalf("Scale: %v", err)
	}

	_, requests := stub.snapshot()
	if len(requests) != 2 {
		t.Fatalf("scale request count = %d, want 2", len(requests))
	}
	if requests[0].JobModifyIndex != 20 || requests[1].JobModifyIndex != 21 {
		t.Fatalf("CAS indexes = %d, %d; want 20, 21", requests[0].JobModifyIndex, requests[1].JobModifyIndex)
	}
}

func TestStatusUsesDurableTaskGroupCount(t *testing.T) {
	t.Parallel()

	stub := newNomadStub(t)
	p := newTestPlugin(t, stub)

	status, err := p.Status(targetConfig())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Ready || status.Count != 3 {
		t.Fatalf("status ready/count = %v/%d", status.Ready, status.Count)
	}
	if status.Meta["nomad_autoscaler.target.nomad.example.stopped"] != "true" {
		t.Fatalf("stopped meta = %#v", status.Meta)
	}
	if status.Meta[sdk.TargetStatusMetaKeyLastEvent] != "12345" {
		t.Fatalf("last event meta = %#v", status.Meta)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
