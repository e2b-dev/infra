//go:build !linux
// +build !linux

package network

import (
	"errors"
)

func (s *Slot) CreateNetwork() error {
	return errors.New("platform does not support network creation")
}

func (s *Slot) RemoveNetwork() error {
	return errors.New("platform does not support network removal")
}
