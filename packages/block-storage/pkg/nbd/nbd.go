package nbd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	gnbd "github.com/akmistry/go-nbd"
)

const (
	nbdDeviceAcquireTimeout = 10 * time.Second
	nbdDeviceAcquireDelay   = 10 * time.Millisecond
)

type Nbd struct {
	device *gnbd.NbdServer
	pool   *NbdDevicePool
	Path   string
}

func (n *Nbd) Close() error {
	disconnectErr := n.device.Disconnect()

	releaseErr := n.pool.ReleaseDevice(n.Path)

	return errors.Join(disconnectErr, releaseErr)
}

func NewNbd(ctx context.Context, s block.Device, pool *NbdDevicePool) (*Nbd, error) {
	nbdDev, err := pool.GetDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get nbd device: %w", err)
	}

	defer func() {
		if err != nil {
			releaseErr := pool.ReleaseDevice(nbdDev)
			if releaseErr != nil {
				fmt.Fprintf(os.Stderr, "failed to release device: %s, %v", nbdDev, releaseErr)
			}
		}
	}()

	nbd := &Nbd{
		Path: nbdDev,
		pool: pool,
	}

	opts := gnbd.BlockDeviceOptions{
		BlockSize:     int(s.BlockSize()),
		ConcurrentOps: 12,
	}

	// Round up to the nearest block size
	size := (s.Size() + s.BlockSize() - 1) / s.BlockSize() * s.BlockSize()

	nbdDevice, err := gnbd.NewServer(nbd.Path, s, size, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create nbd device: %w", err)
	}

	nbd.device = nbdDevice

	return nbd, nil
}

func (n *Nbd) Run() error {
	return n.device.Run()
}
