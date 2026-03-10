package chroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
)

type IsolatedFS struct {
	ns MountNS

	act func(func(billy.Filesystem) error) error
}

var _ billy.Filesystem = (*IsolatedFS)(nil)

func IsolateFileSystem(source string) (*IsolatedFS, error) {
	mountNS, err := TempMountNS()
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary mount namespace: %w", err)
	}

	// use a closure to ensure all requests are executed in the correct namespace
	inner := osfs.New("/")

	fs := &IsolatedFS{
		ns: mountNS,
		act: func(f func(billy.Filesystem) error) error {
			return mountNS.Do(func() error {
				return f(inner)
			})
		},
	}

	if err = fs.chroot(source); err != nil {
		err = fmt.Errorf("failed to chroot into %q: %w", source, err)
		if err2 := mountNS.Close(); err2 != nil {
			err = errors.Join(err, fmt.Errorf("failed to close mount namespace: %w", err2))
		}

		return nil, err
	}

	return fs, nil
}

func (fs *IsolatedFS) chroot(path string) error {
	return fs.ns.Do(func() error {
		var err error

		if err = syscall.Mount("", "/", "", syscall.MS_SLAVE|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("failed to bind mount /: %w", err)
		}

		if err = syscall.Mount(path, path, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("failed to bind mount %q: %w", path, err)
		}

		oldRootPath := filepath.Join(path, "old-path")
		if err = os.MkdirAll(oldRootPath, 0o755); err != nil {
			return fmt.Errorf("failed to create %q: %w", oldRootPath, err)
		}

		if err = syscall.PivotRoot(path, oldRootPath); err != nil {
			return fmt.Errorf("failed to pivot root: %w", err)
		}

		if err = syscall.Chdir("/"); err != nil {
			return fmt.Errorf("failed to chdir: %w", err)
		}

		if err = syscall.Unmount("/old-path", syscall.MNT_DETACH); err != nil {
			return fmt.Errorf("failed to unmount: %w", err)
		}

		if err = os.RemoveAll("/old-path"); err != nil {
			return fmt.Errorf("failed to remove %q: %w", oldRootPath, err)
		}

		return nil
	})
}

func (fs *IsolatedFS) Close() error {
	return fs.ns.Close()
}
