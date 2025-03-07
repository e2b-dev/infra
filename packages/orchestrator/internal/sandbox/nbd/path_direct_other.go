//go:build !linux
// +build !linux

package nbd

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

type DirectPathMount struct {
	Backend block.Device
}

func NewDirectPathMount(b block.Device) *DirectPathMount {

	return nil
}

func (d *DirectPathMount) Open(ctx context.Context) (uint32, error) {
	return 0, errors.New("platform does not support direct path mount")
}

func (d *DirectPathMount) Close() error {
	return errors.New("platform does not support direct path mount")
}
