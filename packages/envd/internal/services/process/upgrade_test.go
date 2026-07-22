package process

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

// spyCgroupManager records Unfreeze calls so we can assert the workload is
// always thawed. Everything else is a no-op.
type spyCgroupManager struct {
	unfreezes atomic.Int64
}

func (m *spyCgroupManager) GetFileDescriptor(cgroups.ProcessType) (int, bool) { return -1, false }
func (m *spyCgroupManager) Freeze(cgroups.ProcessType) error                  { return nil }

func (m *spyCgroupManager) Unfreeze(cgroups.ProcessType) error {
	m.unfreezes.Add(1)

	return nil
}
func (m *spyCgroupManager) Close() error { return nil }

func newHandoverTestService(t *testing.T, spy *spyCgroupManager) *Service {
	t.Helper()
	logger := zerolog.Nop()
	cwd := t.TempDir()

	return newService(&logger, &execcontext.Defaults{
		EnvVars: utils.NewEnvVars(),
		Workdir: &cwd,
	}, cgroups.NewWorkloadFreezer(spy))
}

// TestUpgrade_RejectsUnexpectedBinary verifies the exec target is constrained:
// a caller-supplied path other than the fixed DefaultUpgradeBinPath (or empty
// self-exec) is refused before any side effects, so a malformed/forged upgrade
// request can't turn the same-PID exec into arbitrary code execution.
func TestUpgrade_RejectsUnexpectedBinary(t *testing.T) {
	t.Parallel()

	s := newHandoverTestService(t, &spyCgroupManager{})

	err := s.Upgrade("/tmp/attacker-controlled", "0.6.11", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing upgrade")
}

// TestResumeFromHandoverAlwaysUnfreezes is the safety guarantee: however the
// handover resume ends — a malformed blob (error) or no blob at all — the
// workload is thawed. A failed upgrade must never leave the sandbox frozen.
//
//nolint:paralleltest // mutates the package-global HandoverPath; must run serially
func TestResumeFromHandoverAlwaysUnfreezes(t *testing.T) {
	orig := HandoverPath
	t.Cleanup(func() { HandoverPath = orig })

	// Malformed blob -> ResumeFromHandover errors, but must still thaw.
	spy := &spyCgroupManager{}
	s := newHandoverTestService(t, spy)
	HandoverPath = filepath.Join(t.TempDir(), "handover.json")
	require.NoError(t, os.WriteFile(HandoverPath, []byte("{not valid json"), 0o600))

	_, err := s.ResumeFromHandover(nil)
	require.Error(t, err, "a malformed blob should surface an error")
	assert.Positive(t, spy.unfreezes.Load(), "workload must be thawed even on a bad blob")

	// No blob -> no-op return, but the deferred thaw must still run.
	spy2 := &spyCgroupManager{}
	s2 := newHandoverTestService(t, spy2)
	HandoverPath = filepath.Join(t.TempDir(), "absent.json")

	_, err = s2.ResumeFromHandover(nil)
	require.NoError(t, err)
	assert.Positive(t, spy2.unfreezes.Load(), "the deferred thaw must run on the no-blob path too")
}

// TestResumeFromHandover_ReArmsWatchersBeforeThaw verifies the incoming handover
// re-arms filesystem watchers (via the callback) while the workload is STILL
// frozen — before the deferred thaw — so no filesystem event can be missed in
// the gap between the thaw and the re-arm.
//
//nolint:paralleltest // mutates the package-global HandoverPath; must run serially
func TestResumeFromHandover_ReArmsWatchersBeforeThaw(t *testing.T) {
	orig := HandoverPath
	t.Cleanup(func() { HandoverPath = orig })

	spy := &spyCgroupManager{}
	s := newHandoverTestService(t, spy)
	HandoverPath = filepath.Join(t.TempDir(), "handover.json")
	// A valid blob with no processes: drives the success path through to the
	// watcher-rearm callback.
	require.NoError(t, os.WriteFile(HandoverPath, []byte(`{"from_ver":"0.6.11","processes":[]}`), 0o600))

	unfreezesAtCallback := int64(-1)
	_, err := s.ResumeFromHandover(func([]byte) (int, int) {
		unfreezesAtCallback = spy.unfreezes.Load()

		return 0, 0
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), unfreezesAtCallback, "watchers must be re-armed before any thaw")
	assert.Equal(t, int64(0), spy.unfreezes.Load(), "on success the workload stays frozen for the post-upgrade /init to thaw (so no re-adopted process runs before /init restores auth)")
}
