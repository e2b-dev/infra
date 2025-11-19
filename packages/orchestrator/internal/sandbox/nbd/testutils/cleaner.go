package testutils

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

type Cleaner struct {
	steps []func(ctx context.Context) error
	once  sync.Once
}

func (c *Cleaner) Add(f func(ctx context.Context) error) {
	c.steps = append(c.steps, f)
}

func (c *Cleaner) Run(ctx context.Context, timeout time.Duration) (err error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	c.once.Do(func() {
		var errs []error

		for _, step := range slices.Backward(c.steps) {
			err := step(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to run step: %w", err))
			}
		}

		err = errors.Join(errs...)
	})

	return err
}
