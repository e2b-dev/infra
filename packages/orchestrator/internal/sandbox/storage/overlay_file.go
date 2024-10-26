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

func (o *OverlayFile) Run(sandboxID string) error {
	eg, ctx := errgroup.WithContext(o.ctx)

	eg.Go(func() error {
		err := o.server.Run(ctx)
		if err != nil {
			return fmt.Errorf("error running nbd server: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		err := o.client.Run(ctx, sandboxID)
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

// Path can only be called once.
func (o *OverlayFile) Path(ctx context.Context) (string, error) {
	select {
	case err, ok := <-o.client.Ready:
		if !ok {
			return "", fmt.Errorf("nbd client closed or getting path called multiple times")
		}

		if err != nil {
			return "", fmt.Errorf("error getting nbd path: %w", err)
		}
	case <-ctx.Done():
		return "", ctx.Err()
	}

	return o.client.DevicePath, nil
}
