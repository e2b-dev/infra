package userfaultfd

// SetTestHooks installs the given hooks atomically. Pass nil to clear.
// Only available in test builds (this file is _test.go), so production
// binaries cannot reach it.
func (u *Userfaultfd) SetTestHooks(h *testHooks) {
	u.testHooks.Store(h)
}
