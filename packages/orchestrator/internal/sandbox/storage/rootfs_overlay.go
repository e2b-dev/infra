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

type RootfsOverlay struct {
	device *block_storage.BlockStorageOverlay
	server *nbd.Server
	client *nbd.Client

	ctx       context.Context
	cancelCtx context.CancelCauseFunc
}

func (t *Template) NewRootfsOverlay(cachePath, socketPath string) (*RootfsOverlay, error) {
	rootfs, err := t.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs: %w", err)
	}

	device, err := rootfs.NewOverlay(cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	server := nbd.NewServer(socketPath, func() (block.Device, error) {
		return device, nil
	})

	client, err := server.NewClient(t.nbdPool)
	if err != nil {
		return nil, fmt.Errorf("error creating nbd client: %w", err)
	}

	ctx, cancelCtx := context.WithCancelCause(context.Background())

	return &RootfsOverlay{
		device:    device,
		server:    server,
		client:    client,
		ctx:       ctx,
		cancelCtx: cancelCtx,
	}, nil
}

func (o *RootfsOverlay) Run(sandboxID string) error {
	eg, ctx := errgroup.WithContext(o.ctx)

	eg.Go(func() error {
		err := o.server.Run(ctx)
		if err != nil {
			errMsg := fmt.Errorf("error running nbd server: %w", err)
			o.cancelCtx(errMsg)

			return err
		}

		o.cancelCtx(fmt.Errorf("nbd server exited"))

		return nil
	})

	eg.Go(func() error {
		err := o.client.Run(ctx)
		if err != nil {
			errMsg := fmt.Errorf("error running nbd client: %w", err)
			o.cancelCtx(errMsg)

			return err
		}

		o.cancelCtx(fmt.Errorf("nbd client exited"))

		return nil
	})

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("error waiting for nbd server and client: %w", err)
	}

	return nil
}

func (o *RootfsOverlay) Close() error {
	o.cancelCtx(fmt.Errorf("closing overlay file"))

	// TODO: We should wait for the client and server to close before closing the device.

	err := o.device.Close()
	if err != nil {
		return fmt.Errorf("error closing overlay file: %w", err)
	}

	return nil
}

// Path can only be called once.
func (o *RootfsOverlay) Path(ctx context.Context) (string, error) {
	select {
	case err, ok := <-o.client.Ready:
		if !ok {
			return "", fmt.Errorf("nbd client closed or getting path called multiple times")
		}

		if err != nil {
			return "", fmt.Errorf("error getting nbd path: %w", err)
		}
	case <-o.ctx.Done():
		return "", fmt.Errorf("overlay context closed: %w", errors.Join(o.ctx.Err(), context.Cause(o.ctx)))
	case <-ctx.Done():
		return "", fmt.Errorf("context done: %w", errors.Join(ctx.Err(), context.Cause(ctx)))
	}

	return o.client.DevicePath, nil
}
