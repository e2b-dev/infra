package rootfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync/atomic"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type CowDevice struct {
	overlay block.Device
	mnt     *nbd.DirectPathMount
	cache   *block.Cache

	ready *utils.SetOnce[string]

	blockSize   int64
	BaseBuildId string

	closing atomic.Bool
}

func NewCowDevice(rootfs *template.Storage, cachePath string, blockSize int64) (*CowDevice, error) {
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
		mnt:         mnt,
		overlay:     overlay,
		ready:       utils.NewSetOnce[string](),
		cache:       cache,
		blockSize:   blockSize,
		BaseBuildId: rootfs.Header().Metadata.BaseBuildId.String(),
	}, nil
}

func (o *CowDevice) Start(ctx context.Context) error {
	deviceIndex, err := o.mnt.Open(ctx)
	if err != nil {
		return o.ready.SetError(fmt.Errorf("error opening overlay file: %w", err))
	}

	return o.ready.SetValue(nbd.GetDevicePath(deviceIndex))
}

func (o *CowDevice) Export(out io.Writer, stopSandbox func() error) (*bitset.BitSet, error) {
	if o.closing.CompareAndSwap(false, true) {
		stopSandbox()

		var errs []error

		err := o.close()
		if err != nil {
			return nil, err
		}

		dirty, err := o.cache.Export(out)
		if err != nil {
			errs = append(errs, fmt.Errorf("error exporting cache: %w", err))
		}

		err = o.overlay.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("error closing overlay cache: %w", err))
		}

		err = errors.Join(errs...)
		if err != nil {
			return nil, err
		}

		return dirty, nil
	}

	return nil, nil
}

func (o *CowDevice) Close() error {
	if o.closing.CompareAndSwap(false, true) {
		var errs []error

		err := o.close()
		if err != nil {
			errs = append(errs, err)
		}

		err = o.overlay.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("error closing overlay cache: %w", err))
		}

		return errors.Join(errs...)
	}

	return nil
}

func (o *CowDevice) close() error {
	var errs []error

	err := o.mnt.Close()
	if err != nil {
		errs = append(errs, fmt.Errorf("error closing overlay mount: %w", err))
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

			continue
		}

		if err != nil {
			return fmt.Errorf("error releasing overlay device: %w", err)
		}

		break
	}

	return nil
}

func (o *CowDevice) Path() (string, error) {
	return o.ready.Wait()
}
