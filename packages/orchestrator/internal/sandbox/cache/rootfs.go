package cache

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/block"
)

const ChunkSize = 2 * 1024 * 1024 // 2MiB

type RootfsOverlay struct {
	overlay block.Device
	mnt     *nbd.ManagedPathMount

	ctx       context.Context
	cancelCtx context.CancelFunc

	ready chan string
}

func (t *Template) NewRootfsOverlay(cachePath string) (*RootfsOverlay, error) {
	ctx, cancel := context.WithCancel(context.Background())

	rootfs, err := t.Rootfs()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("error getting rootfs: %w", err)
	}

	overlay, err := block.NewStorageOverlay(rootfs, cachePath)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("error creating overlay: %w", err)
	}

	mnt := nbd.NewManagedPathMount(
		ctx,
		overlay,
		ChunkSize,
	)

	ready := make(chan string, 1)

	return &RootfsOverlay{
		ready:     ready,
		mnt:       mnt,
		overlay:   overlay,
		ctx:       ctx,
		cancelCtx: cancel,
	}, nil
}

func (o *RootfsOverlay) Run() error {
	defer close(o.ready)
	defer o.cancelCtx()

	var wg sync.WaitGroup

	wg.Add(1)

	file, _, err := o.mnt.Open(o.ctx)
	if err != nil {
		return fmt.Errorf("error opening overlay file: %w", err)
	}

	go func() {
		defer wg.Done()

		<-o.ctx.Done()

		err := o.mnt.Close()
		if err != nil {
			log.Printf("error closing overlay mount: %v\n", err)
		}

		err = o.overlay.Close()
		if err != nil {
			log.Printf("error closing overlay cache: %v\n", err)
		}

		counter := 0
		for {
			counter++
			err := nbd.Pool.ReleaseDevice(file)
			if err != nil {
				if counter%100 == 0 {
					log.Printf("[%dth try] error releasing overlay device: %v\n", counter, err)
				}

				continue
			}

			break
		}
	}()

	o.ready <- file

	wg.Wait()

	return o.mnt.Wait()
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
	case path, ok := <-o.ready:
		if !ok {
			return "", fmt.Errorf("overlay path channel closed")
		}

		return path, nil
	}
}
