package sandbox

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Cleanup struct {
	cleanup         []func() error
	priorityCleanup []func() error
	once            sync.Once
	error           error
}

func NewCleanup() *Cleanup {
	return &Cleanup{}
}

func (c *Cleanup) Add(f func() error) {
	c.cleanup = append(c.cleanup, f)
}

func (c *Cleanup) AddPriority(f func() error) {
	c.priorityCleanup = append(c.priorityCleanup, f)
}

func (c *Cleanup) Run() error {
	c.once.Do(c.run)
	return c.error
}

func (c *Cleanup) run() {
	var errs []error

	for i := len(c.priorityCleanup) - 1; i >= 0; i-- {
		err := c.priorityCleanup[i]()
		if err != nil {
			errs = append(errs, err)
		}
	}

	for i := len(c.cleanup) - 1; i >= 0; i-- {
		err := c.cleanup[i]()
		if err != nil {
			errs = append(errs, err)
		}
	}

	c.error = errors.Join(errs...)
}

func cleanupFiles(files *storage.SandboxFiles) error {
	var errs []error

	for _, p := range []string{
		files.SandboxCacheDir(),
		files.SandboxFirecrackerSocketPath(),
		files.SandboxUffdSocketPath(),
		files.SandboxCacheRootfsLinkPath(),
	} {
		err := os.RemoveAll(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to delete '%s': %w", p, err))
		}
	}

	return errors.Join(errs...)
}
