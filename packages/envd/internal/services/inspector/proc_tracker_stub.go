//go:build !linux
// +build !linux

package inspector

type stubProcTracker struct{}

func newProcTracker(_ []string, _ int) procTracker { return &stubProcTracker{} }

func (*stubProcTracker) Reset() (int, bool)        { return 0, false }
func (*stubProcTracker) Query() (bool, bool)       { return true, false }
func (*stubProcTracker) SoftDirtySupported() bool  { return false }
func (*stubProcTracker) BTFPresent() bool          { return false }
func (*stubProcTracker) Close() error              { return nil }
