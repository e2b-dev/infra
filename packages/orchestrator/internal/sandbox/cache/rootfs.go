package cache

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/block"
)

const ChunkSize = 2 * 1024 * 1024 // 2MiB

type RootfsOverlay struct {
	overlay block.Device
	mnt     *nbd.ManagedPathMount

	ctx       context.Context
	cancelCtx context.CancelFunc

	devicePathReady chan string
	nbdReady        chan error
}

func NewRootfsOverlay(template Template, cachePath string) (*RootfsOverlay, error) {
	ctx, cancel := context.WithCancel(context.Background())

	rootfs, err := template.Rootfs()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("error getting rootfs: %w", err)
	}

	overlay, err := block.NewOverlay(rootfs, rootfsBlockSize, cachePath)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	mnt := nbd.NewManagedPathMount(
		ctx,
		overlay,
		ChunkSize,
	)

	return &RootfsOverlay{
		devicePathReady: make(chan string, 1),
		nbdReady:        make(chan error, 1),
		mnt:             mnt,
		overlay:         overlay,
		ctx:             ctx,
		cancelCtx:       cancel,
	}, nil
}

func (o *RootfsOverlay) Run() error {
	defer close(o.devicePathReady)
	defer close(o.nbdReady)
	defer o.cancelCtx()

	deviceIndex, err := nbd.Pool.GetDevice(o.ctx)
	if err != nil {
		return fmt.Errorf("error getting device index: %w", err)
	}

	o.devicePathReady <- nbd.GetDevicePath(deviceIndex)

	_, _, err = o.mnt.Open(o.ctx, deviceIndex)
	if err != nil {
		return fmt.Errorf("error opening overlay file: %w", err)
	}

	o.nbdReady <- nil

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

func (o *RootfsOverlay) Close() {
	o.cancelCtx()
}

// Path can only be called once.
func (o *RootfsOverlay) Path(ctx context.Context) (string, error) {
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

// NbdReady can only be called once.
func (o *RootfsOverlay) NbdReady(ctx context.Context) error {
	select {
	case <-o.ctx.Done():
		return fmt.Errorf("overlay context canceled when getting overlay path: %w", o.ctx.Err())
	case <-ctx.Done():
		return fmt.Errorf("context canceled when getting overlay path: %w", ctx.Err())
	case err, ok := <-o.nbdReady:
		if !ok {
			return fmt.Errorf("overlay nbd ready channel closed")
		}

		return err
	}
}
