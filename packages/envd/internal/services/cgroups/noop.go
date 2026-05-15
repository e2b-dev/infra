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

func (n NoopManager) Thaw(ProcessType) error {
	return nil
}

func (n NoopManager) Close() error {
	return nil
}
