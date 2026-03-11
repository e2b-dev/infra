package chroot

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"syscall"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
)

type IsolatedFS struct {
	ActualRoot string
	Metadata   map[string]string

	ns  *mountNS
	act func(func(billy.Filesystem) error) error
}

var _ billy.Filesystem = (*IsolatedFS)(nil)

type Option func(*IsolatedFS)

func WithMetadata(key, value string) Option {
	return func(fs *IsolatedFS) {
		if fs.Metadata == nil {
			fs.Metadata = make(map[string]string)
		}

		fs.Metadata[key] = value
	}
}

func IsolateFileSystem(ctx context.Context, source string, opts ...Option) (*IsolatedFS, error) {
	mountNS, err := tempMountNS(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary mount namespace: %w", err)
	}

	// use a closure to ensure all requests are executed in the correct namespace
	inner := osfs.New("/")

	fs := &IsolatedFS{
		ActualRoot: source,
		ns:         mountNS,
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

	for _, opt := range opts {
		opt(fs)
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

		for {
			randomDirName := fmt.Sprintf(".old-root.%d", rand.Intn(1000000))

			oldRootPath := filepath.Join(path, randomDirName)
			if err = os.MkdirAll(oldRootPath, 0o755); err != nil {
				if os.IsExist(err) {
					// collided somehow?? retry
					continue
				}

				return fmt.Errorf("failed to create %q: %w", oldRootPath, err)
			}

			if err = syscall.PivotRoot(path, oldRootPath); err != nil {
				return fmt.Errorf("failed to pivot root: %w", err)
			}

			if err = syscall.Chdir("/"); err != nil {
				return fmt.Errorf("failed to chdir: %w", err)
			}

			if err = syscall.Unmount("/"+randomDirName, syscall.MNT_DETACH); err != nil {
				return fmt.Errorf("failed to unmount: %w", err)
			}

			if err = os.Remove("/" + randomDirName); err != nil {
				return fmt.Errorf("failed to remove %q: %w", oldRootPath, err)
			}

			break
		}

		return nil
	})
}

func (fs *IsolatedFS) Close() error {
	return fs.ns.Close()
}
