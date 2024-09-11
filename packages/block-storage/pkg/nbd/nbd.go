package nbd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd/buse"
)

const (
	nbdDeviceAcquireTimeout = 10 * time.Second
	nbdDeviceAcquireDelay   = 10 * time.Millisecond
)

type Nbd struct {
	storage *NbdStorage
	device  *buse.BuseDevice
	pool    *NbdDevicePool
	Path    string
}

type NbdStorage struct {
	storage block.Device
}

func (n *NbdStorage) ReadAt(b []byte, off uint) error {
	_, err := n.storage.ReadAt(b, int64(off))

	return err
}

func (n *NbdStorage) WriteAt(b []byte, off uint) error {
	_, err := n.storage.WriteAt(b, int64(off))

	return err
}

func (n *NbdStorage) Size() uint {
	return uint(n.storage.Size())
}

func (n *NbdStorage) Disconnect() {}

func (n *NbdStorage) Flush() error {
	return n.storage.Sync()
}

func (n *NbdStorage) Trim(off uint, length uint) error {
	return nil
}

func NewNbd(ctx context.Context, s block.Device, pool *NbdDevicePool) (*Nbd, error) {
	nbd := &Nbd{
		storage: &NbdStorage{storage: s},
		pool:    pool,
	}

	nbdCtx, cancel := context.WithTimeout(ctx, nbdDeviceAcquireTimeout)
	defer cancel()

nbdLoop:
	for {
		select {
		case <-nbdCtx.Done():
			return nil, nbdCtx.Err()
		default:
			nbdDev, err := pool.GetDevice()
			if err != nil {
				errMsg := fmt.Sprintf("failed to get nbd device, retrying: %s", err)
				fmt.Fprintf(os.Stderr, errMsg)

				time.Sleep(nbdDeviceAcquireDelay)

				continue
			}

			nbd.Path = nbdDev

			break nbdLoop
		}
	}

	var err error

	defer func() {
		if err != nil {
			pool.ReleaseDevice(nbd.Path)
		}
	}()

	device, err := buse.CreateDevice(nbd.Path, nbd.storage.Size(), nbd.storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create nbd device: %w", err)
	}

	fmt.Printf("created device\n")

	nbd.device = device

	return nbd, nil
}

func (n *Nbd) Run(ctx context.Context) error {
	return n.device.Connect()
}

func (n *Nbd) Stop(ctx context.Context) {
	n.device.Disconnect()
	n.pool.ReleaseDevice(n.Path)
}
