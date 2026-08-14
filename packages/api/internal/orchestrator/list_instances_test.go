package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

// fakeLister is a sandboxNodeLister test double.
type fakeLister struct {
	sandboxes []sandbox.Sandbox
	err       error
}

func (f *fakeLister) GetSandboxes(_ context.Context) ([]sandbox.Sandbox, error) {
	return f.sandboxes, f.err
}

func newTestSandbox(sandboxID string, teamID uuid.UUID, state sandboxtypes.State) sandbox.Sandbox {
	sbx := sandbox.Sandbox{}
	sbx.SandboxID = sandboxID
	sbx.TeamID = teamID
	sbx.State = state
	sbx.StartTime = time.Now()
	sbx.EndTime = time.Now().Add(time.Hour)
	return sbx
}

func TestGetSandboxesFromNodes_NoNodes(t *testing.T) {
	t.Parallel()

	_, err := getSandboxesFromNodes(t.Context(), nil, uuid.New(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no orchestrator nodes connected")
}

func TestGetSandboxesFromNodes_AllNodesFail(t *testing.T) {
	t.Parallel()

	nodes := []namedLister{
		{id: "node-1", lister: &fakeLister{err: errors.New("connection refused")}},
		{id: "node-2", lister: &fakeLister{err: errors.New("timeout")}},
	}

	_, err := getSandboxesFromNodes(t.Context(), nodes, uuid.New(), nil)
	require.Error(t, err)
}

func TestGetSandboxesFromNodes_FiltersByTeam(t *testing.T) {
	t.Parallel()

	teamA := uuid.New()
	teamB := uuid.New()

	nodes := []namedLister{
		{id: "node-1", lister: &fakeLister{sandboxes: []sandbox.Sandbox{
			newTestSandbox("sbx-a1", teamA, sandboxtypes.StateRunning),
			newTestSandbox("sbx-b1", teamB, sandboxtypes.StateRunning),
		}}},
	}

	result, err := getSandboxesFromNodes(t.Context(), nodes, teamA, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "sbx-a1", result[0].SandboxID)
}

func TestGetSandboxesFromNodes_FiltersByState(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	nodes := []namedLister{
		{id: "node-1", lister: &fakeLister{sandboxes: []sandbox.Sandbox{
			newTestSandbox("sbx-running", team, sandboxtypes.StateRunning),
			newTestSandbox("sbx-pausing", team, sandboxtypes.StatePausing),
		}}},
	}

	result, err := getSandboxesFromNodes(t.Context(), nodes, team, []sandboxtypes.State{sandboxtypes.StateRunning})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "sbx-running", result[0].SandboxID)
}

func TestGetSandboxesFromNodes_DeduplicatesAcrossNodes(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	nodes := []namedLister{
		{id: "node-1", lister: &fakeLister{sandboxes: []sandbox.Sandbox{
			newTestSandbox("sbx-dup", team, sandboxtypes.StateRunning),
		}}},
		{id: "node-2", lister: &fakeLister{sandboxes: []sandbox.Sandbox{
			newTestSandbox("sbx-dup", team, sandboxtypes.StateRunning),
			newTestSandbox("sbx-unique", team, sandboxtypes.StateRunning),
		}}},
	}

	result, err := getSandboxesFromNodes(t.Context(), nodes, team, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)

	ids := make(map[string]bool)
	for _, s := range result {
		ids[s.SandboxID] = true
	}
	assert.True(t, ids["sbx-dup"])
	assert.True(t, ids["sbx-unique"])
}

func TestGetSandboxesFromNodes_PartialNodeFailure(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	nodes := []namedLister{
		{id: "node-ok", lister: &fakeLister{sandboxes: []sandbox.Sandbox{
			newTestSandbox("sbx-1", team, sandboxtypes.StateRunning),
		}}},
		{id: "node-fail", lister: &fakeLister{err: errors.New("unreachable")}},
	}

	// Fail-closed: any node failure returns an error to avoid unsafe partial results.
	_, err := getSandboxesFromNodes(t.Context(), nodes, team, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node-fail")
}
