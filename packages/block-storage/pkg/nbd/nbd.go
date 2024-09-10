package nbd

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/samalba/buse-go/buse"
)

type Nbd struct {
	storage *NbdStorage
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

func NewNbd(ctx context.Context, s block.Device) (*Nbd, error) {
	nbd := &Nbd{
		storage: &NbdStorage{storage: s},
	}

	nbdDev := "/dev/nbd0"

	device, err := buse.CreateDevice(nbdDev, nbd.storage.Size(), nbd.storage)
	if err != nil {
		fmt.Printf("Cannot create device: %s\n", err)
		os.Exit(1)
	}

	nbd.Path = nbdDev

	return nbd, nil
}

func (n *Nbd) Stop(ctx context.Context) error {
	return nil
}
