package cgroups

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type Cgroup2Manager struct {
	cgroupFDs map[ProcessType]int
}

var _ Manager = (*Cgroup2Manager)(nil)

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

type Cgroup2Config struct {
	Path       string
	Properties map[string]string
}

func NewCgroup2Manager(opts ...Cgroup2ManagerOption) (*Cgroup2Manager, error) {
	config := cgroup2Config{
		rootPath: "/sys/fs/cgroup",
	}

	for _, opt := range opts {
		opt(&config)
	}

	cgroupFDs, err := createCgroups(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create cgroups: %w", err)
	}

	return &Cgroup2Manager{cgroupFDs: cgroupFDs}, nil
}

func createCgroups(configs cgroup2Config) (map[ProcessType]int, error) {
	var (
		results = make(map[ProcessType]int)
		errs    []error
	)

	for procType, config := range configs.processTypes {
		fullPath := filepath.Join(configs.rootPath, config.Path)
		fd, err := createCgroup(fullPath, config.Properties)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to create %s cgroup: %w", procType, err))

			continue
		}
		results[procType] = fd
	}

	if len(errs) > 0 {
		for procType, fd := range results {
			err := unix.Close(fd)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to close cgroup fd for %s: %w", procType, err))
			}
		}

		return nil, errors.Join(errs...)
	}

	return results, nil
}

func createCgroup(fullPath string, properties map[string]string) (int, error) {
	if err := os.MkdirAll(fullPath, 0o755); err != nil {
		return -1, fmt.Errorf("failed to create cgroup root: %w", err)
	}

	var errs []error
	for name, value := range properties {
		if err := os.WriteFile(filepath.Join(fullPath, name), []byte(value), 0o644); err != nil {
			errs = append(errs, fmt.Errorf("failed to write cgroup property: %w", err))
		}
	}
	if len(errs) > 0 {
		return -1, errors.Join(errs...)
	}

	return unix.Open(fullPath, unix.O_RDONLY, 0)
}

func (c Cgroup2Manager) GetFileDescriptor(procType ProcessType) (int, bool) {
	fd, ok := c.cgroupFDs[procType]

	return fd, ok
}

func (c Cgroup2Manager) Close() error {
	var errs []error
	for procType, fd := range c.cgroupFDs {
		if err := unix.Close(fd); err != nil {
			errs = append(errs, fmt.Errorf("failed to close cgroup fd for %s: %w", procType, err))
		}
		delete(c.cgroupFDs, procType)
	}

	return errors.Join(errs...)
}
