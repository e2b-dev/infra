package userfaultfd

// SetTestFaultHook installs the per-fault test hook atomically. Pass nil to
// clear. Defined here (in a _test.go file) so production binaries cannot
// reach it.
func (u *Userfaultfd) SetTestFaultHook(h func(uintptr, faultPhase)) {
	if h == nil {
		u.testFaultHook.Store(nil)

		return
	}
	u.testFaultHook.Store(&h)
}
