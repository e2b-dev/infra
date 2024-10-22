package storage

import (
	"context"
	"errors"
	"fmt"

	block_storage "github.com/e2b-dev/infra/packages/block-storage/pkg"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"golang.org/x/sync/errgroup"
)

type OverlayFile struct {
	device *block_storage.BlockStorageOverlay
	server *nbd.NbdServer
	client *nbd.NbdClient
}

func NewOverlayFile(
	ctx context.Context,
	storage *block_storage.BlockStorage,
	cachePath string,
	pool *nbd.DevicePool,
	socketPath string,
) (*OverlayFile, error) {
	device, err := storage.CreateOverlay(cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	server, err := nbd.NewNbdServer(ctx, func() (block.Device, error) {
		return device, nil
	}, socketPath)
	if err != nil {
		return nil, fmt.Errorf("error creating nbd: %w", err)
	}

	client := server.CreateClient(ctx, pool)

	return &OverlayFile{
		device: device,
		server: server,
		client: client,
	}, nil
}

func (o *OverlayFile) Run() error {
	eg := errgroup.Group{}

	eg.Go(func() error {
		err := o.server.Start()
		if err != nil {
			return fmt.Errorf("error starting nbd server: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		err := o.client.Start()
		if err != nil {
			return fmt.Errorf("error starting nbd client: %w", err)
		}

		return nil
	})

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("error starting overlay file: %w", err)
	}

	return nil
}

func (o *OverlayFile) Close() error {
	var errs []error

	err := o.client.Close()
	if err != nil {
		errs = append(errs, err)
	}

	err = o.server.Close()
	if err != nil {
		errs = append(errs, err)
	}

	err = o.device.Close()
	if err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (o *OverlayFile) Path() (string, error) {
	return o.client.GetPath()
}
