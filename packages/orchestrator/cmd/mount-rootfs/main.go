package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var _ block.ReadonlyDevice = (*mountReadonlyDevice)(nil)

type mountReadonlyDevice struct {
	*build.File

	header    *header.Header
	blockSize int64
}

func newReadonlyDevice(file *build.File, header *header.Header, blockSize int64) *mountReadonlyDevice {
	return &mountReadonlyDevice{
		File:      file,
		header:    header,
		blockSize: blockSize,
	}
}

func (m *mountReadonlyDevice) Close() error {
	return nil
}

func (m *mountReadonlyDevice) BlockSize() int64 {
	return m.blockSize
}

func (m *mountReadonlyDevice) Header() *header.Header {
	return m.header
}

func (m *mountReadonlyDevice) Size() (int64, error) {
	return int64(m.header.Metadata.Size), nil
}

type loggedOverlay struct {
	overlay *block.Overlay
}

func (l *loggedOverlay) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stdout, "[read panic recovered]: [%d, %d] -> %v\n", off, len(p), r)
		}
	}()

	fmt.Fprintf(os.Stdout, "[read started]: [%d, %d]\n", off, len(p))

	n, err := l.overlay.ReadAt(ctx, p, off)

	fmt.Fprintf(os.Stdout, "[read completed]: [%d, %d] -> %d\n", off, len(p), n)

	return n, err
}

func (l *loggedOverlay) WriteAt(p []byte, off int64) (int, error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stdout, "[write panic recovered]: [%d, %d] -> %v\n", off, len(p), r)
		}
	}()

	fmt.Fprintf(os.Stdout, "[write started]: [%d, %d]\n", off, len(p))

	n, err := l.overlay.WriteAt(p, off)

	fmt.Fprintf(os.Stdout, "[write completed]: [%d, %d] -> %d\n", off, len(p), n)

	return n, err
}

func (l *loggedOverlay) Size() (int64, error) {
	return l.overlay.Size()
}

func (l *loggedOverlay) BlockSize() int64 {
	return l.overlay.BlockSize()
}

func (l *loggedOverlay) Header() *header.Header {
	return l.overlay.Header()
}

func (l *loggedOverlay) Close() error {
	return l.overlay.Close()
}

func (l *loggedOverlay) EjectCache() (*block.Cache, error) {
	return l.overlay.EjectCache()
}

func (l *loggedOverlay) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	return l.overlay.Slice(ctx, off, length)
}

func main() {
	buildId := flag.String("build", "", "build id")
	mountPath := flag.String("mount", "", "mount path")
	verify := flag.Bool("verify", false, "verify rootfs integrity")

	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	// Disabling the logger for normal use—is very spammy, because Populate on device pool periodically logs errors if the number of acquirable devices is less than the number of requested devices.
	// logger, err := zap.NewDevelopment()
	// if err != nil {
	// 	log.Fatalf("failed to create logger: %s", err)
	// }
	// zap.ReplaceGlobals(logger)

	go func() {
		<-done

		cancel()
	}()

	// We use a separate ctx for majority of the operations as cancelling context for the NBD+storage and *then* doing cleanup for these often resulted in deadlocks.
	nbdContext := context.Background()

	err := mountRootfs(ctx, nbdContext, *buildId, *mountPath, *verify)
	if err != nil {
		panic(fmt.Errorf("failed to mount rootfs: %w", err))
	}
}

func mountRootfs(ctx, nbdContext context.Context, buildID, mountPath string, verify bool) error {
	files := storage.TemplateFiles{
		BuildID: buildID,
	}

	s, err := storage.GetTemplateStorageProvider(nbdContext, nil)
	if err != nil {
		return fmt.Errorf("failed to get storage provider: %w", err)
	}

	obj, err := s.OpenObject(nbdContext, files.StorageRootfsHeaderPath(), storage.RootFSHeaderObjectType)
	if err != nil {
		return fmt.Errorf("failed to open object: %w", err)
	}

	h, err := header.Deserialize(nbdContext, obj)
	if err != nil {
		id, err := uuid.Parse(buildID)
		if err != nil {
			return fmt.Errorf("failed to parse build id: %w", err)
		}

		r, err := s.OpenSeekableObject(nbdContext, files.StorageRootfsPath(), storage.RootFSObjectType)
		if err != nil {
			return fmt.Errorf("failed to open object: %w", err)
		}

		size, err := r.Size(nbdContext)
		if err != nil {
			return fmt.Errorf("failed to get object size: %w", err)
		}

		h, err = header.NewHeader(&header.Metadata{
			BuildId:     id,
			BaseBuildId: id,
			Size:        uint64(size),
			Version:     1,
			BlockSize:   header.RootfsBlockSize,
			Generation:  1,
		}, nil)
		if err != nil {
			return fmt.Errorf("failed to create header for rootfs without header: %w", err)
		}
	}

	diffCacheDir := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.diff.cache-%s", buildID, uuid.New().String()))

	err = os.MkdirAll(diffCacheDir, 0o755)
	if err != nil {
		return fmt.Errorf("failed to create diff cache directory: %w", err)
	}

	defer os.RemoveAll(diffCacheDir)

	flags, err := featureflags.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create feature flags client: %w", err)
	}

	store, err := build.NewDiffStore(
		nbdContext,
		cfg.Config{},
		flags,
		diffCacheDir,
		24*time.Hour,
		24*time.Hour,
	)
	if err != nil {
		return fmt.Errorf("failed to create diff store: %w", err)
	}

	defer store.Close()

	fmt.Printf("caching diffs to: %+v\n", diffCacheDir)

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}

	rootfs := build.NewFile(h, store, build.Rootfs, s, m)

	readonlyDevice := newReadonlyDevice(rootfs, h, int64(h.Metadata.BlockSize))

	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.ext4.cow.cache-%s", buildID, uuid.New().String()))

	defer os.RemoveAll(cowCachePath)

	cache, err := block.NewCache(
		int64(h.Metadata.Size),
		int64(h.Metadata.BlockSize),
		cowCachePath,
		false,
	)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}

	fmt.Printf("caching writes to: %+v\n", cowCachePath)

	overlay := block.NewOverlay(readonlyDevice, cache)
	defer overlay.Close()

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("failed to create device pool: %w", err)
	}

	poolClosed := make(chan struct{})

	defer func() { //nolint:contextcheck // we need to use separate context otherwise the cleanup can be problematic
		<-poolClosed

		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()

		err = devicePool.Close(cleanupCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close device pool: %v\n", err)
		}
	}()

	poolCtx, poolCancel := context.WithCancel(nbdContext)
	defer poolCancel()

	go func() {
		devicePool.Populate(poolCtx)
		close(poolClosed)
	}()

	mnt := nbd.NewDirectPathMount(&loggedOverlay{overlay}, devicePool)

	mntIndex, err := mnt.Open(nbdContext)
	if err != nil {
		return fmt.Errorf("failed to open nbd mount: %w", err)
	}

	defer func() { //nolint:contextcheck // we need to use separate context otherwise the cleanup can be problematic
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()

		err = mnt.Close(cleanupCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close nbd mount: %v\n", err)
		}
	}()

	devicePath := nbd.GetDevicePath(mntIndex)

	fmt.Printf("rootfs exposed as device: %s\n", devicePath)

	if mountPath != "" {
		err = os.MkdirAll(mountPath, 0o755)
		if err != nil {
			return fmt.Errorf("failed to create mount path directory: %w", err)
		}

		fmt.Fprintf(os.Stdout, "creating mount path directory: %s\n", mountPath)

		// We don't remote the dir as it might have been user created.

		err = unix.Mount(devicePath, mountPath, "ext4", 0, "")
		if err != nil {
			return fmt.Errorf("failed to mount device to mount path: %w", err)
		}

		defer func() {
			ticker := time.NewTicker(600 * time.Millisecond)
			defer ticker.Stop()

			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()

			for {
				select {
				case <-cleanupCtx.Done():
					fmt.Fprintf(os.Stderr, "failed to unmount device from mount path in time\n")

					return
				case <-ticker.C:
					err = unix.Unmount(mountPath, 0)
					if err == nil {
						return
					}

					fmt.Fprintf(os.Stderr, "failed to unmount device from mount path: %v\n", err)
				}
			}
		}()

		fmt.Printf("rootfs mounted at path: %s\n", mountPath)
	}

	// cmd := exec.CommandContext(ctx, "dd", "if=/dev/zero", "of="+devicePath, "bs=4k", "count=1", "oflag=direct")

	// cmd.Stdout = os.Stdout
	// cmd.Stderr = os.Stderr

	// err = cmd.Run()
	// if err != nil {
	// 	return fmt.Errorf("failed to write zero to device (with direct flag): %w", err)
	// }

	// fmt.Println("> zero written to device (with direct flag)")

	// d, err := os.OpenFile(devicePath, unix.O_DIRECT|unix.O_RDWR, 0)
	// if err != nil {
	// 	return fmt.Errorf("failed to open device: %w", err)
	// }
	// defer d.Close()

	// buf := make([]byte, 4096)

	// // fmt.Println("mmapped buffer start", unsafe.Pointer(&buf[0]))
	// _, err = d.WriteAt(buf, 0)
	// if err != nil {
	// 	return fmt.Errorf("failed to write zero to device: %w", err)
	// }

	// fmt.Println("zero written to device")

	if verify {
		fmt.Println("\nverifying rootfs integrity...")

		cmd := exec.CommandContext(ctx, "e2fsck", "-nfv", devicePath)

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to verify rootfs integrity: %w", err)
		}

		fmt.Println("\nrootfs integrity verified")

		journalDir := filepath.Join(mountPath, "var", "log", "journal")
		journalFiles, err := os.ReadDir(journalDir)
		if err != nil {
			return fmt.Errorf("failed to read journal directory: %w", err)
		}

		for _, journalFile := range journalFiles {
			cmd := exec.CommandContext(ctx, "journalctl", "--verify", "--directory", filepath.Join(journalDir, journalFile.Name()))
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			err := cmd.Run()
			if err != nil {
				return fmt.Errorf("failed to verify journal file: %w", err)
			}
		}

		fmt.Println("\njournal files verified")

		return nil
	}

	<-ctx.Done()

	fmt.Println("closing rootfs mount")

	return nil
}
