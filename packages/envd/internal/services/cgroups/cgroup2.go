//go:build linux

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

	// Verify cgroup v2 is available by checking the filesystem type.
	// On cgroup v1, /sys/fs/cgroup is a tmpfs and directories/files can be
	// created freely, causing Cgroup2Manager to "succeed" with invalid fds
	// that the kernel rejects with EBADF on clone3(CLONE_INTO_CGROUP).
	var st unix.Statfs_t
	if err := unix.Statfs(config.rootPath, &st); err != nil {
		return nil, fmt.Errorf("failed to statfs cgroup root %s: %w", config.rootPath, err)
	}
	if st.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, fmt.Errorf("cgroup root %s is not a cgroup2 filesystem (type=0x%x)", config.rootPath, st.Type)
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

// writeCgroupProp writes value to a cgroupfs property file without O_CREATE
// so missing properties surface as ENOENT/EACCES (cgroup kernfs has no
// create inode op) instead of being silently created on tmpfs fallback.
func writeCgroupProp(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(value)
	cerr := f.Close()
	if werr != nil {
		return werr
	}

	return cerr
}

func createCgroup(fullPath string, properties map[string]string) (int, error) {
	if err := os.MkdirAll(fullPath, 0o755); err != nil {
		return -1, fmt.Errorf("failed to create cgroup root: %w", err)
	}

	var errs []error
	for name, value := range properties {
		if err := writeCgroupProp(filepath.Join(fullPath, name), value); err != nil {
			// Tolerate properties whose controller isn't enabled in subtree_control.
			// cgroup kernfs returns ENOENT or EACCES depending on which controller is missing.
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				fmt.Fprintf(os.Stderr, "cgroup property %q unavailable at %q, skipping\n", name, fullPath)

				continue
			}
			errs = append(errs, fmt.Errorf("failed to write cgroup property %q: %w", name, err))
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
