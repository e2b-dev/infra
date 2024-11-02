package local_storage

import (
	"context"
	"fmt"
	"os"
	"sync"

	template "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
	"github.com/pojntfx/r3map/pkg/mount"
)

const ChunkSize = 2 * 1024 * 1024 // 2MiB

type RootfsOverlay struct {
	storage    *template.BlockStorage
	mnt        *mount.ManagedPathMount
	localCache *os.File

	ctx       context.Context
	cancelCtx context.CancelFunc

	ready chan string
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

	mnt := mount.NewManagedPathMount(
		ctx,
		t.Rootfs,
		backend.NewFileBackend(f),
		&mount.ManagedMountOptions{
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

func (o *RootfsOverlay) Run(sandboxID string) error {
	defer close(o.ready)
	defer o.cancelCtx()

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		<-o.ctx.Done()

		o.mnt.Close()

		o.localCache.Close()
	}()

	file, _, err := o.mnt.Open()
	if err != nil {
		return fmt.Errorf("error opening overlay file: %w", err)
	}

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
