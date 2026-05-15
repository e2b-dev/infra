//go:build !linux

package cgroups

// Cgroup2Manager is a non-functional stub on non-Linux platforms.
// cgroups v2 is a Linux-only kernel feature.
type Cgroup2Manager struct{}

var _ Manager = (*Cgroup2Manager)(nil)

type Cgroup2Config struct {
	Path       string
	Properties map[string]string
}

type cgroup2Config struct {
	rootPath     string
	processTypes map[ProcessType]Cgroup2Config
}

type Cgroup2ManagerOption func(*cgroup2Config)

func WithCgroup2RootSysFSPath(path string) Cgroup2ManagerOption {
	return func(config *cgroup2Config) {
		config.rootPath = path
	}
}

func WithCgroup2ProcessType(processType ProcessType, path string, properties map[string]string) Cgroup2ManagerOption {
	return func(config *cgroup2Config) {
		if config.processTypes == nil {
			config.processTypes = make(map[ProcessType]Cgroup2Config)
		}
		config.processTypes[processType] = Cgroup2Config{Path: path, Properties: properties}
	}
}

func NewCgroup2Manager(_ ...Cgroup2ManagerOption) (*Cgroup2Manager, error) {
	return &Cgroup2Manager{}, nil
}

func (c Cgroup2Manager) GetFileDescriptor(ProcessType) (int, bool) {
	return 0, false
}

func (c Cgroup2Manager) Freeze(ProcessType) error {
	return nil
}

func (c Cgroup2Manager) Thaw(ProcessType) error {
	return nil
}

func (c Cgroup2Manager) Close() error {
	return nil
}
