package userfaultfd

// mockFd is a mock implementation of the Fd interface.
// It allows us to test the handling methods separately from the actual uffd serve loop.
type mockFd struct {
	// The channels send back the info about the uffd handled operations
	// and also allows us to block the methods to test the flow.
	copyCh                  chan UffdioCopy
	removeWriteProtectionCh chan UffdioWriteProtect
}

func newMockFd() *mockFd {
	return &mockFd{
		copyCh:                  make(chan UffdioCopy),
		removeWriteProtectionCh: make(chan UffdioWriteProtect),
	}
}

func (m *mockFd) register(addr uintptr, size uint64, mode CULong) error {
	return nil
}

func (m *mockFd) unregister(addr, size uintptr) error {
	return nil
}

func (m *mockFd) copy(addr, pagesize uintptr, data []byte, mode CULong) error {
	m.copyCh <- newUffdioCopy(nil, CULong(addr), CULong(pagesize), mode, CLong(len(data)))

	return nil
}

func (m *mockFd) removeWriteProtection(addr, size uintptr) error {
	m.removeWriteProtectionCh <- newUffdioWriteProtect(CULong(addr), CULong(size), 0)

	return nil
}

func (m *mockFd) addWriteProtection(addr, size uintptr) error {
	return nil
}

func (m *mockFd) close() error {
	return nil
}

func (m *mockFd) fd() int32 {
	return 0
}
