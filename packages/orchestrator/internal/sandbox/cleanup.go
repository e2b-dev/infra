package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Cleanup struct {
	cleanup         []func(ctx context.Context) error
	priorityCleanup []func(ctx context.Context) error
	error           error
	once            sync.Once
}

func NewCleanup() *Cleanup {
	return &Cleanup{}
}

func (c *Cleanup) Add(f func(ctx context.Context) error) {
	c.cleanup = append(c.cleanup, f)
}

func (c *Cleanup) AddPriority(f func(ctx context.Context) error) {
	c.priorityCleanup = append(c.priorityCleanup, f)
}

func (c *Cleanup) Run(ctx context.Context) error {
	c.once.Do(func() {
		c.run(ctx)
	})
	return c.error
}

func (c *Cleanup) run(ctx context.Context) {
	var errs []error

	for i := len(c.priorityCleanup) - 1; i >= 0; i-- {
		err := c.priorityCleanup[i](ctx)
		if err != nil {
			errs = append(errs, err)
		}
	}

	for i := len(c.cleanup) - 1; i >= 0; i-- {
		err := c.cleanup[i](ctx)
		if err != nil {
			errs = append(errs, err)
		}
	}

	c.error = errors.Join(errs...)
}

func cleanupFiles(files *storage.SandboxFiles) error {
	var errs []error

	for _, p := range []string{
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
