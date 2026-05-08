//go:build !linux || !inspector_bpf
// +build !linux !inspector_bpf

package inspector

import "context"

// stubFsTracker is the build-tag-disabled fallback. It reports
// degraded=true so the service keeps falling through to a full
// checkpoint on the orchestrator side.
type stubFsTracker struct{}

func newFsTracker() fsTracker { return &stubFsTracker{} }

func (*stubFsTracker) Start(_ context.Context) error  { return nil }
func (*stubFsTracker) AddCgroup(_ uint64) error       { return nil }
func (*stubFsTracker) RemoveCgroup(_ uint64) error    { return nil }
func (*stubFsTracker) Query() (uint64, bool)          { return 0, false }
func (*stubFsTracker) Reset() (uint64, bool)          { return 0, false }
func (*stubFsTracker) Close() error                   { return nil }
