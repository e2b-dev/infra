package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/google/uuid"
)

type DeviceWithClose struct {
	*build.File
	id   string
	size int64
}

func (d *DeviceWithClose) Close() error {
	return nil
}

func (d *DeviceWithClose) Size() (int64, error) {
	return d.size, nil
}

func (d *DeviceWithClose) ReadAt(p []byte, off int64) (int, error) {
	return d.File.ReadAt(p, off)
}

const (
	contentFileName = "test.txt"
	content         = ``
	blockSize       = 4096
)

// Execute the passed callback with the passed overlay mounted as a nbd device.
func executeForNbd(
	ctx context.Context,
	overlay *block.Overlay,
	cb func(mountedPath string) error,
) error {
	mnt := nbd.NewDirectPathMount(overlay)

	nbdCtx, nbdCancel := context.WithCancel(ctx)
	defer nbdCancel()

	go func() {
		<-nbdCtx.Done()

		mnt.Close()
	}()

	mntIndex, err := mnt.Open(nbdCtx)
	if err != nil {
		return fmt.Errorf("failed to open: %w", err)
	}

	devicePath := nbd.GetDevicePath(mntIndex)

	fmt.Printf("- created nbd device at %s\n", devicePath)

	// Check the block device health
	out, err := exec.CommandContext(nbdCtx, "fsck.ext4", "-n", devicePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to fsck: %w %s", err, out)
	}

	fmt.Printf("- fscked base nbd device at %s\n", out)

	tmpDir, err := os.MkdirTemp("", "mount-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}

	defer os.RemoveAll(tmpDir)

	out, err = exec.CommandContext(nbdCtx, "mount", devicePath, tmpDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to mount: %w - %s", err, out)
	}

	defer func() {
		out, err = exec.Command("umount", tmpDir).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to umount: %s - %s", err, out)
		}
	}()

	fmt.Printf("- mounted rootfs at %s\n", devicePath)

	err = cb(tmpDir)
	if err != nil {
		return fmt.Errorf("failed execute: %w", err)
	}

	return nil
}

func fileFromStorage(
	ctx context.Context,
	baseBuildId string,
	store *build.DiffStore,
) (*build.File, *header.Header, error) {
	buildId, err := uuid.Parse(baseBuildId)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	object := gcs.NewObject(ctx, gcs.TemplateBucket, buildId.String()+"/"+string(build.Rootfs))

	size, err := object.Size()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get object size: %w", err)
	}

	header := header.NewHeader(&header.Metadata{
		BuildId:     buildId,
		BaseBuildId: buildId,
		Size:        uint64(size),
		Version:     1,
		BlockSize:   uint64(blockSize),
		Generation:  1,
	}, nil)

	rootfs := build.NewFile(header, store, build.Rootfs)

	return rootfs, header, nil
}

// Create an overlay by extracting the diff from the passed overlay,
// putting it into the store and then creating a mapping that combines the diff and the base to a new overlay.
func fileFromOverlay(
	overlay *block.Overlay,
	baseHeader *header.Header,
	store *build.DiffStore,
) (*build.File, *header.Header, error) {
	diffBuildId := uuid.New()

	// TODO: Diff file is not cleaned up after exit. The same goes for the base storage file in the cache.
	// This should not affect the test.
	diffFile, err := build.NewLocalDiffFile(diffBuildId.String(), build.Rootfs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create diff file: %w", err)
	}

	cache, err := overlay.EjectCache()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to eject cache: %w", err)
	}

	dirtyBlocks, err := cache.Export(diffFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to export cache: %w", err)
	}

	diff, err := diffFile.ToDiff(blockSize)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert diff file to diff: %w", err)
	}

	newMappings := header.CreateMapping(
		baseHeader.Metadata,
		&diffBuildId,
		dirtyBlocks,
	)

	diffMappings := header.MergeMappings(
		baseHeader.Mapping,
		newMappings,
	)

	diffHeader := header.NewHeader(&header.Metadata{
		BuildId:     diffBuildId,
		BaseBuildId: baseHeader.Metadata.BaseBuildId,
		Size:        baseHeader.Metadata.Size,
		Version:     baseHeader.Metadata.Version,
		BlockSize:   baseHeader.Metadata.BlockSize,
	}, diffMappings)

	store.Add(diffHeader.Metadata.BuildId.String(), build.Rootfs, diff)

	// Create a build file that will use the diff and the base already in the store
	diffRootfs := build.NewFile(diffHeader, store, build.Rootfs)

	return diffRootfs, diffHeader, nil
}

func compareSources(
	s1,
	s2 io.ReaderAt,
	diffBuildId *uuid.UUID,
	mappings []*header.BuildMap,
) error {
	for _, mapping := range mappings {
		if mapping.BuildId.String() != diffBuildId.String() {
			continue
		}

		for off := mapping.Offset; off < mapping.Offset+mapping.Length; off += blockSize {
			c1 := make([]byte, blockSize)
			n1, err := s1.ReadAt(c1, int64(off))
			if err != nil {
				return fmt.Errorf("failed to read content1: %w", err)
			}

			c2 := make([]byte, blockSize)
			n2, err := s2.ReadAt(c2, int64(off))
			if err != nil {
				return fmt.Errorf("failed to read content2: %w", err)
			}

			if n1 != n2 {
				return fmt.Errorf("content length mismatch: %d != %d", n1, n2)
			}

			if !bytes.Equal(c1, c2) {
				// Hash the content and show the mismatch
				h1 := sha256.Sum256(c1)
				h2 := sha256.Sum256(c2)

				return fmt.Errorf("content mismatch (showing hashes):\n%x != %x\n, offset: %d, length: %d", h1, h2, mapping.Offset, mapping.Length)
			}
		}
	}

	return nil
}

func checkNbd(ctx context.Context, buildId string) error {
	store, err := build.NewDiffStore(gcs.TemplateBucket, ctx)
	if err != nil {
		return fmt.Errorf("failed to create diff store: %w", err)
	}

	baseRootfs, baseHeader, err := fileFromStorage(ctx, buildId, store)
	if err != nil {
		return fmt.Errorf("failed to create base overlay: %w", err)
	}

	baseCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("cache-base-rootfs.ext4-%s", baseHeader.Metadata.BuildId))
	baseCache, err := block.NewCache(int64(baseHeader.Metadata.Size), blockSize, baseCachePath, false)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	defer baseCache.Close()

	baseOverlay := block.NewOverlay(
		&DeviceWithClose{baseRootfs, "base", int64(baseHeader.Metadata.Size)},
		baseCache,
		blockSize,
	)

	defer baseOverlay.Close()

	fmt.Printf("\n----- Base overlay mount -----\n\n")

	// Modify content in the overlay mounted from the base rootfs
	err = executeForNbd(ctx, baseOverlay, func(mountedPath string) error {
		contentPath := filepath.Join(mountedPath, contentFileName)

		err = os.WriteFile(contentPath, []byte(content), 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}

		// fmt.Printf("- writing to content Path: %s\n", contentPath)

		// out, err := exec.CommandContext(ctx, "sync").CombinedOutput()
		// if err != nil {
		// 	return fmt.Errorf("failed to sync: %w - %s", err, out)
		// }

		// out, err = exec.CommandContext(ctx, "/bin/bash", "-c", "echo 3 | sudo tee /proc/sys/vm/drop_caches").CombinedOutput()
		// if err != nil {
		// 	return fmt.Errorf("failed to sync: %w - %s", err, out)
		// }

		return nil
	})

	diffRootfs, diffHeader, err := fileFromOverlay(baseOverlay, baseHeader, store)
	if err != nil {
		return fmt.Errorf("failed to create diff overlay: %w", err)
	}

	fmt.Printf("\n----- Diff header -----\n")

	for _, mapping := range diffHeader.Mapping {
		fmt.Println(mapping.Format(baseHeader.Metadata.BlockSize))
	}

	// Compare the changed parts in the base overlay with the content in the diff overlay.
	err = compareSources(
		baseOverlay,
		diffRootfs,
		&diffHeader.Metadata.BuildId,
		diffHeader.Mapping,
	)
	if err != nil {
		return fmt.Errorf("failed to compare overlays: %w", err)
	} else {
		fmt.Println("overlay comparison successful")
	}

	diffCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("diff-cache-rootfs.ext4-%s", diffHeader.Metadata.BuildId))

	diffCache, err := block.NewCache(int64(baseHeader.Metadata.Size), blockSize, diffCachePath, false)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	defer diffCache.Close()

	diffOverlay := block.NewOverlay(
		&DeviceWithClose{diffRootfs, "diff", int64(baseHeader.Metadata.Size)},
		diffCache,
		blockSize,
	)
	defer diffOverlay.Close()

	fmt.Printf("\n----- Diff overlay mount -----\n\n")

	// Check the modified content in the overlay created from the diff and the base
	err = executeForNbd(ctx, diffOverlay, func(mountedPath string) error {
		contentPath := filepath.Join(mountedPath, contentFileName)

		readContent, err := os.ReadFile(contentPath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		fmt.Printf("- reading from content Path: %s\n", contentPath)
		fmt.Printf("- content: %s\n", readContent)

		if string(readContent) != content {
			return fmt.Errorf("content mismatch: %s\n", readContent)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to mount nbd: %w", err)
	}

	return nil
}

func main() {
	buildId := flag.String("build", "", "template build id")

	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

	fmt.Printf("\n================== ROOTFS DIFF TEST ===================")

	err := checkNbd(ctx, *buildId)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n\n => failed:\n %s\n\n", err)
	} else {
		fmt.Println("\n\n => success")
	}
}
