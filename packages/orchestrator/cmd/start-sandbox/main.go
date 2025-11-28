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
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func main() {
	buildId := flag.String("build", "", "build id")
	sandboxId := flag.String("sandbox", "", "sandbox id (defaults to random UUID)")
	logging := flag.Bool("log", false, "enable logging (it is pretty spammy)")

	flag.Parse()

	if *buildId == "" {
		log.Fatalf("build id is required")
	}

	if *sandboxId == "" {
		*sandboxId = uuid.New().String()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	// Logger is very spammy, because Populate on device pool periodically logs errors if the number of acquirable devices is less than the number of requested devices.
	if *logging {
		l, err := logger.NewDevelopmentLogger()
		if err != nil {
			panic(fmt.Errorf("failed to create logger: %w", err))
		}
		logger.ReplaceGlobals(ctx, l)
	}

	go func() {
		<-done
		cancel()
	}()

	// We use a separate ctx for majority of the operations as cancelling context for the NBD+storage and *then* doing cleanup for these often resulted in deadlocks.
	nbdContext := context.Background()

	err := run(ctx, nbdContext, *buildId, *sandboxId)
	if err != nil {
		panic(fmt.Errorf("failed to start sandbox: %w", err))
	}
}

func run(ctx, nbdContext context.Context, buildID, sandboxID string) error {
	// Get template rootfs
	rootfsDevice, rootfsCleanup, err := testutils.TemplateRootfs(ctx, buildID)
	defer rootfsCleanup.Run(ctx, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to get template rootfs: %w", err)
	}

	// Get template memfile
	memfileDevice, memfileCleanup, err := templateMemfile(ctx, buildID)
	defer memfileCleanup.Run(ctx, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to get template memfile: %w", err)
	}

	// Create device pool for NBD
	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("failed to create device pool: %w", err)
	}
	defer devicePool.Close(ctx)

	// Create rootfs cache path
	cachePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.ext4.cow.cache-%s", buildID, uuid.New().String()))
	defer os.RemoveAll(cachePath)

	// Create rootfs provider
	rootfsProvider, err := rootfs.NewNBDProvider(
		rootfsDevice,
		cachePath,
		devicePool,
	)
	if err != nil {
		return fmt.Errorf("failed to create rootfs provider: %w", err)
	}
	defer rootfsProvider.Close(ctx)

	// Start rootfs provider
	go func() {
		runErr := rootfsProvider.Start(nbdContext)
		if runErr != nil {
			logger.L().Error(ctx, "rootfs provider error", logger.WithSandboxID(sandboxID), zap.Error(runErr))
		}
	}()

	// Get rootfs path
	rootfsPath, err := rootfsProvider.Path()
	if err != nil {
		return fmt.Errorf("failed to get rootfs path: %w", err)
	}
	fmt.Printf("rootfs exposed as device: %s\n", rootfsPath)

	// Create uffd socket path
	uffdSocketPath := filepath.Join(os.TempDir(), fmt.Sprintf("uffd-%s.sock", sandboxID))
	defer os.RemoveAll(uffdSocketPath)

	// Create and start uffd
	fcUffd, err := uffd.New(memfileDevice, uffdSocketPath, memfileDevice.BlockSize())
	if err != nil {
		return fmt.Errorf("failed to create uffd: %w", err)
	}

	if err = fcUffd.Start(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to start uffd: %w", err)
	}
	defer func() {
		if stopErr := fcUffd.Stop(); stopErr != nil {
			logger.L().Error(ctx, "failed to stop uffd", logger.WithSandboxID(sandboxID), zap.Error(stopErr))
		}
	}()

	fmt.Printf("uffd socket created at: %s\n", uffdSocketPath)

	// Create network slot
	networkConfig, err := network.ParseConfig()
	if err != nil {
		return fmt.Errorf("failed to parse network config: %w", err)
	}

	// Create memory storage for network slots
	slotStorage, err := network.NewStorageMemory(network.GetVrtSlotsSize(), networkConfig)
	if err != nil {
		return fmt.Errorf("failed to create network slot storage: %w", err)
	}

	// Acquire a network slot
	slot, err := slotStorage.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire network slot: %w", err)
	}
	defer func() {
		if releaseErr := slotStorage.Release(slot); releaseErr != nil {
			logger.L().Error(ctx, "failed to release network slot", logger.WithSandboxID(sandboxID), zap.Error(releaseErr))
		}
	}()

	// Create network
	err = slot.CreateNetwork(ctx)
	if err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}
	defer func() {
		if removeErr := slot.RemoveNetwork(); removeErr != nil {
			logger.L().Error(ctx, "failed to remove network", logger.WithSandboxID(sandboxID), zap.Error(removeErr))
		}
	}()

	fmt.Printf("network created: namespace=%s, host_ip=%s, vpeer_ip=%s, veth_ip=%s\n",
		slot.NamespaceID(), slot.HostIPString(), slot.VpeerIP().String(), slot.VethIP().String())

	fmt.Println("sandbox started successfully")
	fmt.Println("press Ctrl+C to stop")

	<-ctx.Done()

	fmt.Println("closing sandbox")

	return nil
}

func templateMemfile(ctx context.Context, buildID string) (*testutils.BuildDevice, *testutils.Cleaner, error) {
	var cleaner testutils.Cleaner

	files := storage.TemplateFiles{
		BuildID: buildID,
	}

	s, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to get storage provider: %w", err)
	}

	obj, err := s.OpenObject(ctx, files.StorageMemfileHeaderPath(), storage.MemfileHeaderObjectType)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to open object: %w", err)
	}

	h, err := header.Deserialize(ctx, obj)
	if err != nil {
		id, err := uuid.Parse(buildID)
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to parse build id: %w", err)
		}

		r, err := s.OpenSeekableObject(ctx, files.StorageMemfilePath(), storage.MemfileObjectType)
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to open object: %w", err)
		}

		size, err := r.Size(ctx)
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to get object size: %w", err)
		}

		// Default memfile block size (hugepage size)
		blockSize := uint64(header.HugepageSize)
		h, err = header.NewHeader(&header.Metadata{
			BuildId:     id,
			BaseBuildId: id,
			Size:        uint64(size),
			Version:     1,
			BlockSize:   blockSize,
			Generation:  1,
		}, nil)
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to create header for memfile without header: %w", err)
		}
	}

	diffCacheDir := filepath.Join(os.TempDir(), fmt.Sprintf("%s-memfile.diff.cache-%s", buildID, uuid.New().String()))

	err = os.MkdirAll(diffCacheDir, 0o755)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create diff cache directory: %w", err)
	}

	cleaner.Add(func(context.Context) error {
		return os.RemoveAll(diffCacheDir)
	})

	flags, err := featureflags.NewClient()
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create feature flags client: %w", err)
	}

	store, err := build.NewDiffStore(
		cfg.Config{},
		flags,
		diffCacheDir,
		24*time.Hour,
		24*time.Hour,
	)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create diff store: %w", err)
	}

	store.Start(ctx)

	cleaner.Add(func(context.Context) error {
		store.RemoveCache()

		return nil
	})

	cleaner.Add(func(context.Context) error {
		store.Close()

		return nil
	})

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create metrics: %w", err)
	}

	buildDevice := testutils.NewBuildDevice(
		build.NewFile(h, store, build.Memfile, s, m),
		h,
		int64(h.Metadata.BlockSize),
	)

	return buildDevice, &cleaner, nil
}
