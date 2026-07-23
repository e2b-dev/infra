package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildlogger"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// newSetOnce is a convenience wrapper for the generic SetOnce constructor.
func newSetOnce() *utils.SetOnce[BuildInfoResult] {
	return utils.NewSetOnce[BuildInfoResult]()
}

// newTestBuildCache creates a BuildCache with a noop meter provider for testing.
func newTestBuildCache(t *testing.T) *BuildCache {
	t.Helper()
	ctx := context.Background()
	bc := NewBuildCache(ctx, noop.NewMeterProvider())
	t.Cleanup(func() { bc.cache.Stop() })
	return bc
}

// newTestLogs creates a LogEntryLogger for testing.
func newTestLogs() *buildlogger.LogEntryLogger {
	return buildlogger.NewLogEntryLogger()
}

// ─── BuildInfo state machine ──────────────────────────────────────────────────

func TestBuildInfo_InitialStateIsBuilding(t *testing.T) {
	t.Parallel()
	info := &BuildInfo{
		TeamID: "team-1",
		logs:   newTestLogs(),
		Result: newSetOnce(),
	}

	assert.Equal(t, template_manager.TemplateBuildState_Building, info.GetStatus())
	assert.True(t, info.IsRunning())
	assert.Nil(t, info.GetResult())
}

func TestBuildInfo_SetSuccess(t *testing.T) {
	t.Parallel()
	info := &BuildInfo{
		TeamID: "team-1",
		logs:   newTestLogs(),
		Result: newSetOnce(),
	}

	meta := &template_manager.TemplateBuildMetadata{EnvdVersionKey: "1.0.0"}
	info.SetSuccess(meta)

	assert.Equal(t, template_manager.TemplateBuildState_Completed, info.GetStatus())
	assert.False(t, info.IsRunning())

	result := info.GetResult()
	require.NotNil(t, result)
	assert.Equal(t, template_manager.TemplateBuildState_Completed, result.Status)
	assert.Equal(t, meta, result.Metadata)
	assert.Nil(t, result.Reason)
}

func TestBuildInfo_SetFail(t *testing.T) {
	t.Parallel()
	info := &BuildInfo{
		TeamID: "team-1",
		logs:   newTestLogs(),
		Result: newSetOnce(),
	}

	reason := &template_manager.TemplateBuildStatusReason{Message: "out of disk"}
	info.SetFail(reason)

	assert.Equal(t, template_manager.TemplateBuildState_Failed, info.GetStatus())
	assert.False(t, info.IsRunning())

	result := info.GetResult()
	require.NotNil(t, result)
	assert.Equal(t, template_manager.TemplateBuildState_Failed, result.Status)
	assert.Equal(t, reason, result.Reason)
	assert.Nil(t, result.Metadata)
}

func TestBuildInfo_SetFailWithNilReason(t *testing.T) {
	t.Parallel()
	info := &BuildInfo{
		TeamID: "team-1",
		logs:   newTestLogs(),
		Result: newSetOnce(),
	}

	// Should not panic when reason is nil.
	info.SetFail(nil)

	assert.Equal(t, template_manager.TemplateBuildState_Failed, info.GetStatus())
}

func TestBuildInfo_SetSuccessIdempotent(t *testing.T) {
	t.Parallel()
	info := &BuildInfo{
		TeamID: "team-1",
		logs:   newTestLogs(),
		Result: newSetOnce(),
	}

	meta1 := &template_manager.TemplateBuildMetadata{EnvdVersionKey: "1.0.0"}
	meta2 := &template_manager.TemplateBuildMetadata{EnvdVersionKey: "2.0.0"}

	info.SetSuccess(meta1)
	info.SetSuccess(meta2) // second call should be silently ignored

	result := info.GetResult()
	require.NotNil(t, result)
	// First value wins.
	assert.Equal(t, meta1, result.Metadata)
}

func TestBuildInfo_GetLogs(t *testing.T) {
	t.Parallel()
	info := &BuildInfo{
		TeamID: "team-1",
		logs:   newTestLogs(),
		Result: newSetOnce(),
	}

	logs := info.GetLogs()
	assert.NotNil(t, logs)
	assert.Empty(t, logs)
}

// ─── BuildCache CRUD ──────────────────────────────────────────────────────────

func TestBuildCache_CreateAndGet(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	info, err := bc.Create("team-1", "build-abc", newTestLogs())
	require.NoError(t, err)
	require.NotNil(t, info)

	got, err := bc.Get("build-abc")
	require.NoError(t, err)
	assert.Equal(t, info, got)
}

func TestBuildCache_GetMissingReturnsError(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	_, err := bc.Get("nonexistent-build")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-build")
}

func TestBuildCache_CreateDuplicateReturnsError(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	_, err := bc.Create("team-1", "build-dup", newTestLogs())
	require.NoError(t, err)

	_, err = bc.Create("team-1", "build-dup", newTestLogs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build-dup")
}

func TestBuildCache_DeleteRemovesEntry(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	_, err := bc.Create("team-1", "build-del", newTestLogs())
	require.NoError(t, err)

	bc.Delete("build-del")

	_, err = bc.Get("build-del")
	require.Error(t, err)
}

func TestBuildCache_DeleteNonExistentIsNoOp(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	// Should not panic.
	bc.Delete("does-not-exist")
}

func TestBuildCache_MultipleBuildsIsolated(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	info1, err := bc.Create("team-1", "build-1", newTestLogs())
	require.NoError(t, err)

	info2, err := bc.Create("team-2", "build-2", newTestLogs())
	require.NoError(t, err)

	info1.SetSuccess(&template_manager.TemplateBuildMetadata{EnvdVersionKey: "v1"})

	// build-2 should still be running.
	got2, err := bc.Get("build-2")
	require.NoError(t, err)
	assert.True(t, got2.IsRunning())
	assert.Equal(t, info2, got2)
}

func TestBuildCache_GetAfterDeleteReturnsError(t *testing.T) {
	t.Parallel()
	bc := newTestBuildCache(t)

	_, err := bc.Create("team-1", "build-x", newTestLogs())
	require.NoError(t, err)

	bc.Delete("build-x")

	_, err = bc.Get("build-x")
	require.Error(t, err)
}
