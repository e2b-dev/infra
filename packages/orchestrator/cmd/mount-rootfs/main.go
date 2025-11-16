package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

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

func mountRootfs(ctx context.Context, buildID string) error {
	files := storage.TemplateFiles{
		BuildID: buildID,
	}

	s, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get storage provider: %w", err)
	}

	obj, err := s.OpenObject(ctx, files.StorageRootfsHeaderPath(), storage.RootFSHeaderObjectType)
	if err != nil {
		return fmt.Errorf("failed to open object: %w", err)
	}

	h, err := header.Deserialize(ctx, obj)
	if err != nil {
		id, err := uuid.Parse(buildID)
		if err != nil {
			return fmt.Errorf("failed to parse build id: %w", err)
		}

		files.StorageRootfsPath()

		r, err := s.OpenSeekableObject(ctx, files.StorageRootfsPath(), storage.RootFSObjectType)
		if err != nil {
			return fmt.Errorf("failed to open object: %w", err)
		}

		size, err := r.Size(ctx)
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
			return fmt.Errorf("failed to create header: %w", err)
		}
	}

	random := uuid.New().String()
	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.ext4.cow.cache-%s", buildID, random))

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

	random = uuid.New().String()
	diffCacheDir := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.diff.cache-%s", buildID, random))

	err = os.MkdirAll(diffCacheDir, 0o755)
	if err != nil {
		return fmt.Errorf("failed to create diff cache directory: %w", err)
	}

	defer os.RemoveAll(diffCacheDir)

	fmt.Printf("caching diffs to: %+v\n", diffCacheDir)

	flags, err := featureflags.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create feature flags client: %w", err)
	}

	store, err := build.NewDiffStore(
		ctx,
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

	rootfs := build.NewFile(h, store, build.Rootfs, s, metrics.Metrics{})

	readonlyDevice := newReadonlyDevice(rootfs, h, int64(h.Metadata.BlockSize))

	overlay := block.NewOverlay(readonlyDevice, cache)
	defer overlay.Close()

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("failed to create device pool: %w", err)
	}

	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()

		err = devicePool.Close(cleanupCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close device pool: %v\n", err)
		}
	}()

	go func() {
		devicePool.Populate(ctx)
	}()

	mnt := nbd.NewDirectPathMount(overlay, devicePool)

	mntIndex, err := mnt.Open(ctx)
	if err != nil {
		return fmt.Errorf("failed to open nbd mount: %w", err)
	}

	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()

		err = mnt.Close(cleanupCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close nbd mount: %v\n", err)
		}
	}()

	devicePath := nbd.GetDevicePath(mntIndex)

	fmt.Printf("rootfs mounted at path:\n%s\n", devicePath)

	<-ctx.Done()

	fmt.Println("closing rootfs mount")

	return nil
}

func main() {
	buildId := flag.String("build", "", "build id")

	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	// set global zap logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("failed to create logger: %s", err)
	}
	zap.ReplaceGlobals(logger)

	go func() {
		<-done

		cancel()
	}()

	err = mountRootfs(ctx, *buildId)
	if err != nil {
		log.Fatalf("failed to mount rootfs: %s", err)
	}
}
