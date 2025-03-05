//go:build !linux
// +build !linux

package nbd

import (
	"context"
	"errors"
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

type DirectPathMount struct {
	Backend     block.Device
	ctx         context.Context
	dispatcher  *Dispatch
	conn        net.Conn
	deviceIndex uint32
	blockSize   uint64
	cancelfn    context.CancelFunc
}

func NewDirectPathMount(b block.Device) *DirectPathMount {
	ctx, cancelfn := context.WithCancel(context.Background())

	return &DirectPathMount{
		Backend:   b,
		ctx:       ctx,
		cancelfn:  cancelfn,
		blockSize: 4096,
	}
}

func (d *DirectPathMount) Open(ctx context.Context) (uint32, error) {
	return 0, errors.New("platform does not support direct path mount")
}

func (d *DirectPathMount) Close() error {
	return errors.New("platform does not support direct path mount")
}
