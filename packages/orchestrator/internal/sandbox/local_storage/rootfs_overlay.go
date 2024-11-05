package local_storage

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage/nbd"
	template "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
)

const ChunkSize = 2 * 1024 * 1024 // 2MiB

type RootfsOverlay struct {
	storage    *template.BlockStorage
	mnt        *nbd.ManagedPathMount
	localCache *os.File

	ctx       context.Context
	cancelCtx context.CancelFunc

	ready chan string
}

func createPriorityFunction(size int64) func(off int64) int64 {
	middleCeiling := int(math.Ceil(float64(size) / 2))

	return func(off int64) int64 {
		distanceFromMiddle := int64(math.Abs(float64(off - int64(middleCeiling))))

		return distanceFromMiddle
	}
}

func (t *Template) NewRootfsOverlay(cachePath string) (*RootfsOverlay, error) {
	ctx, cancel := context.WithCancel(context.Background())

	f, err := os.Create(cachePath)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("error creating overlay file: %w", err)
	}

	size, err := t.Rootfs.Size()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("error getting rootfs size: %w", err)
	}

	err = f.Truncate(size)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("error truncating overlay file: %w", err)
	}

	mnt := nbd.NewManagedPathMount(
		ctx,
		t.Rootfs,
		backend.NewFileBackend(f),
		&nbd.ManagedMountOptions{
			ChunkSize: ChunkSize,
			Verbose:   true,
		},
		nil,
		&server.Options{
			MinimumBlockSize:   rootfsBlockSize,
			MaximumBlockSize:   rootfsBlockSize,
			PreferredBlockSize: rootfsBlockSize,
		},
		&client.Options{
			BlockSize: rootfsBlockSize,
		},
	)

	ready := make(chan string, 1)

	return &RootfsOverlay{
		ready:      ready,
		mnt:        mnt,
		localCache: f,
		storage:    t.Rootfs,
		ctx:        ctx,
		cancelCtx:  cancel,
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

		fmt.Printf("[%s] Closing overlay\n", time.Now().Format(time.RFC3339))
		err := o.mnt.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error closing overlay mount: %v\n", err)
		}

		err = o.localCache.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error closing overlay file: %v\n", err)
		}

		err = os.Remove(o.localCache.Name())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error removing overlay file: %v\n", err)
		}

		for {
			err := nbd.Pool.ReleaseDevice(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error releasing overlay device: %v\n", err)
				continue
			}

			fmt.Fprintf(os.Stderr, "released overlay device\n")

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
