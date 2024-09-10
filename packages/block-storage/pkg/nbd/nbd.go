package nbd

import (
	"context"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
)

type Nbd struct {
	device block.Device
	Path   string
}

func NewNbd(ctx context.Context, device block.Device) (*Nbd, error) {
	nbd := &Nbd{
		device: device,
	}

	return nbd, nil
}

func (n *Nbd) Stop(ctx context.Context) error {
	return nil
}
