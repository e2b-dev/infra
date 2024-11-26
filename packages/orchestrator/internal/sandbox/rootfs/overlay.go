package rootfs

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
)

const BlockSize = 2 << 11

type Overlay struct {
	overlay block.Device
	mnt     *nbd.ManagedPathMount

	ctx       context.Context
	cancelCtx context.CancelFunc

	devicePathReady chan string
}

func NewOverlay(rootfs block.ReadonlyDevice, cachePath string) (*Overlay, error) {
	ctx, cancel := context.WithCancel(context.Background())

	overlay, err := block.NewOverlay(rootfs, BlockSize, cachePath)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	mnt := nbd.NewManagedPathMount(
		ctx,
		overlay,
	)

	return &Overlay{
		devicePathReady: make(chan string, 1),
		mnt:             mnt,
		overlay:         overlay,
		ctx:             ctx,
		cancelCtx:       cancel,
	}, nil
}

func (o *Overlay) Run() error {
	defer close(o.devicePathReady)
	defer o.cancelCtx()

	deviceIndex, _, err := o.mnt.Open(o.ctx)
	if err != nil {
		return fmt.Errorf("error opening overlay file: %w", err)
	}

	o.devicePathReady <- nbd.GetDevicePath(deviceIndex)

	<-o.ctx.Done()

	err = o.mnt.Close()
	if err != nil {
		return fmt.Errorf("error closing overlay mount: %w", err)
	}

	err = o.overlay.Close()
	if err != nil {
		return fmt.Errorf("error closing overlay cache: %w", err)
	}

	counter := 0
	for {
		counter++
		err := nbd.Pool.ReleaseDevice(deviceIndex)
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

func (o *Overlay) Close() {
	o.cancelCtx()
}

// Path can only be called once.
func (o *Overlay) Path(ctx context.Context) (string, error) {
	select {
	case <-o.ctx.Done():
		return "", fmt.Errorf("overlay context canceled when getting overlay path: %w", o.ctx.Err())
	case <-ctx.Done():
		return "", fmt.Errorf("context canceled when getting overlay path: %w", ctx.Err())
	case path, ok := <-o.devicePathReady:
		if !ok {
			return "", fmt.Errorf("overlay path channel closed")
		}

		return path, nil
	}
}
