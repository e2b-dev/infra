//go:build linux

package sandbox

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TestSandboxHandle is a handle returned by NewTestSandbox that lets tests
// control the sandbox lifecycle (signal exit, trigger cleanup).
// Only intended for use in tests.
type TestSandboxHandle struct {
	Sbx  *Sandbox
	exit *utils.ErrorOnce
}

// SignalExit unblocks Sandbox.Wait() as if the sandbox process exited cleanly.
func (h *TestSandboxHandle) SignalExit() {
	_ = h.exit.SetSuccess()
}

// SignalExitWithError unblocks Sandbox.Wait() with an error.
func (h *TestSandboxHandle) SignalExitWithError(err error) {
	_ = h.exit.SetError(err)
}

// NewTestSandbox creates a minimal Sandbox suitable for unit tests.
// The returned handle lets callers control when the sandbox "exits".
func NewTestSandbox(sandboxID string) *TestSandboxHandle {
	exit := utils.NewErrorOnce()
	cleanup := NewCleanup()

	sbx := &Sandbox{
		LifecycleID: sandboxID + "-lifecycle",
		Metadata: &Metadata{
			Runtime: RuntimeMetadata{SandboxID: sandboxID},
			Config:  NewConfig(Config{}),
		},
		Resources: &Resources{},
		exit:      exit,
		cleanup:   cleanup,
	}

	return &TestSandboxHandle{
		Sbx:  sbx,
		exit: exit,
	}
}
