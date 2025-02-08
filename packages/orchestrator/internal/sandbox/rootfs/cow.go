package rootfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type CowDevice struct {
	overlay *block.Overlay
	mnt     *nbd.DirectPathMount

	ready *utils.SetOnce[string]

	blockSize   int64
	BaseBuildId string

	finishedOperations chan struct{}

	logger logging.Logger
}

func NewCowDevice(logger logging.Logger, rootfs *template.Storage, cachePath string, blockSize int64) (*CowDevice, error) {
	size, err := rootfs.Size()
	if err != nil {
		return nil, fmt.Errorf("error getting device size: %w", err)
	}

	cache, err := block.NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := block.NewOverlay(rootfs, cache, blockSize)

	mnt := nbd.NewDirectPathMount(overlay)

	return &CowDevice{
		mnt:                mnt,
		overlay:            overlay,
		ready:              utils.NewSetOnce[string](),
		blockSize:          blockSize,
		finishedOperations: make(chan struct{}, 1),
		BaseBuildId:        rootfs.Header().Metadata.BaseBuildId.String(),
		logger:             logger,
	}, nil
}

func (o *CowDevice) Start(ctx context.Context) error {
	deviceIndex, err := o.mnt.Open(ctx)
	if err != nil {
		return o.ready.SetError(fmt.Errorf("error opening overlay file: %w", err))
	}

	return o.ready.SetValue(nbd.GetDevicePath(deviceIndex))
}

func (o *CowDevice) Export(ctx context.Context, out io.Writer, stopSandbox func() error) (*bitset.BitSet, error) {
	cache, err := o.overlay.EjectCache()
	if err != nil {
		return nil, fmt.Errorf("error ejecting cache: %w", err)
	}

	// the error is already logged in go routine in SandboxCreate handler
	go stopSandbox()

	select {
	case <-o.finishedOperations:
		break
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for overlay device to be released")
	}

	dirty, err := cache.Export(out)
	if err != nil {
		return nil, fmt.Errorf("error exporting cache: %w", err)
	}

	err = cache.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing cache: %w", err)
	}

	return dirty, nil
}

func (o *CowDevice) Close() error {
	var errs []error

	err := o.mnt.Close()
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay mount: %w", err))
	}

	o.finishedOperations <- struct{}{}

	err = o.overlay.Close()
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay cache: %w", err))
	}

	devicePath, err := o.ready.Wait()
	if err != nil {
		errs = append(errs, fmt.Errorf("error getting overlay path: %w", err))

		return errors.Join(errs...)
	}

	slot, err := nbd.GetDeviceSlot(devicePath)
	if err != nil {
		errs = append(errs, fmt.Errorf("error getting overlay slot: %w", err))

		return errors.Join(errs...)
	}

	counter := 0
	for {
		counter++
		err := nbd.Pool.ReleaseDevice(slot)
		if errors.Is(err, nbd.ErrDeviceInUse{}) {
			if counter%100 == 0 {
				log.Printf("[%dth try] error releasing overlay device: %v\n", counter, err)
			}

			time.Sleep(500 * time.Millisecond)

			continue
		}

		if err != nil {
			return fmt.Errorf("error releasing overlay device: %w", err)
		}

		break
	}

	o.logger.Infof("overlay device released")

	return nil
}

func (o *CowDevice) Path() (string, error) {
	return o.ready.Wait()
}
