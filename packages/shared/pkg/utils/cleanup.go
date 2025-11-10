package utils

import (
	"errors"
	"slices"
)

type Cleaner struct {
	cleanups []func() error
}

func (c *Cleaner) Add(f func() error) {
	c.cleanups = append(c.cleanups, f)
}

func (c *Cleaner) Run() error {
	var errs []error

	slices.Reverse(c.cleanups)

	for _, cleanup := range c.cleanups {
		err := cleanup()
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
