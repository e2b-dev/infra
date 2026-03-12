package chrooted

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type Chrooted struct {
	ActualRoot string
	Metadata   map[string]string

	mu  sync.RWMutex
	ns  *mountNS
	act func(func() error) error
}

type Option func(*Chrooted)

func WithMetadata(key, value string) Option {
	return func(fs *Chrooted) {
		if fs.Metadata == nil {
			fs.Metadata = make(map[string]string)
		}

		fs.Metadata[key] = value
	}
}

func Chroot(ctx context.Context, source string, opts ...Option) (*Chrooted, error) {
	mountNS, err := tempMountNS(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary mount namespace: %w", err)
	}

	fs := &Chrooted{
		ActualRoot: source,
		ns:         mountNS,
	}

	fs.act = func(f func() error) error {
		fs.mu.RLock()
		defer fs.mu.RUnlock()

		return mountNS.Do(func() error {
			return f()
		})
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

const maxMountAttempts = 10

var ErrFailedToMount = errors.New("retries exhausted")

func (fs *Chrooted) chroot(path string) error {
	return fs.ns.Do(func() error {
		var err error

		if err = syscall.Mount("", "/", "", syscall.MS_SLAVE|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("failed to bind mount /: %w", err)
		}

		if err = syscall.Mount(path, path, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("failed to bind mount %q: %w", path, err)
		}

		for range maxMountAttempts {
			randomDirName := fmt.Sprintf(".old-root.%d", rand.Intn(1000000))

			oldRootPath := filepath.Join(path, randomDirName)
			if err = os.Mkdir(oldRootPath, 0o755); err != nil {
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

			return nil
		}

		return ErrFailedToMount
	})
}

func (fs *Chrooted) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	return fs.ns.Close()
}
