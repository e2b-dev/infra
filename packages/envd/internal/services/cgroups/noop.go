package cgroups

type NoopManager struct{}

var _ Manager = (*NoopManager)(nil)

func NewNoopManager() *NoopManager {
	return &NoopManager{}
}

func (n NoopManager) GetFileDescriptor(ProcessType) (int, bool) {
	return 0, false
}

func (n NoopManager) Freeze(ProcessType) error {
	return nil
}

func (n NoopManager) Unfreeze(ProcessType) error {
	return nil
}

// Frozen reports that freeze state is unobservable: with no cgroups there is nothing
// to read, and no caller should wait for a state that can never appear. Reporting
// (false, nil) instead would make every freeze look like a workload refusing to stop,
// which costs a full wait budget per pause and blocks the live-upgrade handover
// outright.
func (n NoopManager) Frozen(ProcessType) (bool, error) {
	return false, ErrFrozenUnobservable
}

func (n NoopManager) Close() error {
	return nil
}
