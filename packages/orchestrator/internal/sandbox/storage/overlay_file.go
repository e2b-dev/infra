package storage

import (
	"context"
	"fmt"

	block_storage "github.com/e2b-dev/infra/packages/block-storage/pkg"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"golang.org/x/sync/errgroup"
)

type OverlayFile struct {
	device *block_storage.BlockStorageOverlay
	server *nbd.Server
	client *nbd.Client

	ctx       context.Context
	cancelCtx context.CancelFunc
}

func NewOverlayFile(
	storage *block_storage.BlockStorage,
	cachePath string,
	pool *nbd.DevicePool,
	socketPath string,
) (*OverlayFile, error) {
	device, err := storage.NewOverlay(cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	server := nbd.NewServer(socketPath, func() (block.Device, error) {
		return device, nil
	})

	client, err := server.NewClient(pool)
	if err != nil {
		return nil, fmt.Errorf("error creating nbd client: %w", err)
	}

	ctx, cancelCtx := context.WithCancel(context.Background())

	return &OverlayFile{
		device:    device,
		server:    server,
		client:    client,
		ctx:       ctx,
		cancelCtx: cancelCtx,
	}, nil
}

func (o *OverlayFile) Run() error {
	eg, ctx := errgroup.WithContext(o.ctx)

	eg.Go(func() error {
		defer o.cancelCtx()

		err := o.server.Run(ctx)
		if err != nil {
			return fmt.Errorf("error running nbd server: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		defer o.cancelCtx()

		err := o.client.Run(ctx)
		if err != nil {
			return fmt.Errorf("error running nbd client: %w", err)
		}

		return nil
	})

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("error waiting for nbd server and client: %w", err)
	}

	return nil
}

func (o *OverlayFile) Close() error {
	o.cancelCtx()

	// TODO: We should wait for the client and server to close before closing the device.

	err := o.device.Close()
	if err != nil {
		return fmt.Errorf("error closing overlay file: %w", err)
	}

	return nil
}

// NbdPath can only be called once.
func (o *OverlayFile) NbdPath(ctx context.Context) (string, error) {
	select {
	case err := <-o.client.Ready:
		if err != nil {
			return "", fmt.Errorf("error getting nbd path: %w", err)
		}
	case <-o.ctx.Done():
		return "", o.ctx.Err()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	return o.client.DevicePath, nil
}
