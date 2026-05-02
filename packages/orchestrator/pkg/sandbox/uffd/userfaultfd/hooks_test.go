package userfaultfd

// SetTestFaultHook installs the per-fault hook atomically; pass nil to
// clear. Lives in a _test.go file so production binaries cannot link it.
func (u *Userfaultfd) SetTestFaultHook(h func(uintptr, faultPhase)) {
	if h == nil {
		u.testFaultHook.Store(nil)

		return
	}
	u.testFaultHook.Store(&h)
}
