package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	block_storage "github.com/e2b-dev/infra/packages/block-storage/pkg"
	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
)

const (
	overlayFileTimeout = 5 * time.Second
)

type OverlayFile struct {
	overlay *block_storage.BlockStorageOverlay
	nbd     *nbd.Nbd
}

func NewOverlayFile(ctx context.Context, storage *block_storage.BlockStorage, cachePath string, pool *nbd.NbdDevicePool) (*OverlayFile, error) {
	overlay, err := storage.CreateOverlay(cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, overlayFileTimeout)
	defer cancel()

	n, err := nbd.NewNbd(ctx, overlay, pool)
	if err != nil {
		return nil, fmt.Errorf("error creating nbd: %w", err)
	}

	return &OverlayFile{
		overlay: overlay,
		nbd:     n,
	}, nil
}

func (o *OverlayFile) Run() error {
	err := o.nbd.Run()
	if err != nil {
		return fmt.Errorf("error running nbd: %w", err)
	}

	return nil
}

func (o *OverlayFile) Close() error {
	err := o.nbd.Close()

	overlayErr := o.overlay.Close()

	return errors.Join(err, overlayErr)
}
