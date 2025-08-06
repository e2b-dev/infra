package fdexit

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// FdExit is a wrapper around a pipe that allows to signal the exit of the uffd.
type FdExit struct {
	r    *os.File
	w    *os.File
	exit func() error
}

func New() (*FdExit, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create exit fd: %w", err)
	}

	return &FdExit{
		r: r,
		w: w,
		exit: sync.OnceValue(func() error {
			_, writeErr := w.Write([]byte{0})
			if writeErr != nil {
				return fmt.Errorf("failed write to exit writer: %w", writeErr)
			}

			return nil
		}),
	}, nil
}

func (e *FdExit) SignalExit() error {
	return e.exit()
}

func (e *FdExit) Reader() int32 {
	return int32(e.r.Fd())
}

func (e *FdExit) Close() error {
	return errors.Join(e.r.Close(), e.w.Close())
}
