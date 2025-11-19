package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd/testutils"
)

func main() {
	buildId := flag.String("build", "", "build id")
	mountPath := flag.String("mount", "", "mount path")
	verify := flag.Bool("verify", false, "verify rootfs integrity")
	logging := flag.Bool("log", false, "enable logging (it is pretty spammy)")

	flag.Parse()

	if *verify && *mountPath == "" {
		log.Fatalf("verify flag is only supported when mount path is provided")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	// Logger is very spammy, because Populate on device pool periodically logs errors if the number of acquirable devices is less than the number of requested devices.
	if *logging {
		logger, err := zap.NewDevelopment()
		if err != nil {
			log.Fatalf("failed to create logger: %s", err)
		}
		zap.ReplaceGlobals(logger)
	}

	go func() {
		<-done

		cancel()
	}()

	// We use a separate ctx for majority of the operations as cancelling context for the NBD+storage and *then* doing cleanup for these often resulted in deadlocks.
	nbdContext := context.Background()

	err := run(ctx, nbdContext, *buildId, *mountPath, *verify)
	if err != nil {
		panic(fmt.Errorf("failed to mount rootfs: %w", err))
	}
}

func run(ctx, nbdContext context.Context, buildID, mountPath string, verify bool) error {
	cleanupCtx := context.Background() //nolint:contextcheck // we need to use separate context otherwise the cleanup can be problematic

	rootfs, rootfsCleanup, err := testutils.TemplateRootfs(ctx, buildID)
	defer rootfsCleanup.Run(cleanupCtx)
	if err != nil {
		return fmt.Errorf("failed to get template rootfs: %w", err)
	}

	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.ext4.cow.cache-%s", buildID, uuid.New().String()))

	defer os.RemoveAll(cowCachePath)

	cache, err := block.NewCache(
		int64(rootfs.Header().Metadata.Size),
		int64(rootfs.Header().Metadata.BlockSize),
		cowCachePath,
		false,
	)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}

	fmt.Printf("caching writes to: %+v\n", cowCachePath)

	overlay := block.NewOverlay(rootfs, cache)
	defer overlay.Close()

	devicePath, deviceCleanup, err := testutils.GetNBDDevice(nbdContext, overlay)
	defer deviceCleanup.Run(cleanupCtx)
	if err != nil {
		return fmt.Errorf("failed to get nbd device: %w", err)
	}

	fmt.Printf("rootfs exposed as device: %s\n", devicePath)

	if mountPath != "" {
		err = os.MkdirAll(mountPath, 0o755)
		if err != nil {
			return fmt.Errorf("failed to create mount path directory: %w", err)
		}

		fmt.Fprintf(os.Stdout, "creating mount path directory: %s\n", mountPath)

		mountCleanup, err := testutils.MountNBDDevice(devicePath, mountPath)
		defer mountCleanup.Run(cleanupCtx)
		if err != nil {
			return fmt.Errorf("failed to mount device to mount path: %w", err)
		}

		// We don't remove the dir as it might have been user created.

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
