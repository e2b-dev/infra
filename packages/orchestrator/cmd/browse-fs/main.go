package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/masahiro331/go-ext4-filesystem/ext4"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd/testutils"
)

func main() {
	buildId := flag.String("build", "", "build id")
	cat := flag.String("cat", "", "cat file from build")
	ls := flag.String("ls", "", "list files in build")

	flag.Parse()

	if *cat != "" && *ls != "" {
		log.Fatalf("only one of -cat or -ls can be used")
	}

	if *cat == "" && *ls == "" {
		log.Fatalf("one of -cat or -ls must be used")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

	filesystem, rootfsCleanup, err := createFS(ctx, *buildId)
	defer rootfsCleanup.Run(ctx, 30*time.Second)
	if err != nil {
		panic(fmt.Errorf("failed to create ext4 filesystem: %w", err))
	}

	if *cat != "" {
		fmt.Fprintf(os.Stdout, "Reading file: %s\n", *cat)

		data, err := fs.ReadFile(filesystem, *cat)
		if err != nil {
			panic(fmt.Errorf("failed to read file: %w", err))
		}

		fmt.Println(string(data))
	}

	if *ls != "" {
		dir, err := filesystem.ReadDir(*ls)
		if err != nil {
			panic(fmt.Errorf("failed to read directory: %w", err))
		}

		fmt.Fprintf(os.Stdout, "Listing directory: %s\n", *ls)

		for _, entry := range dir {
			fmt.Fprintf(os.Stdout, "%s\n", entry.Name())
		}
	}
}

type readerAtWithoutCtx struct {
	testutils.BuildDevice

	ctx context.Context
}

func (r *readerAtWithoutCtx) ReadAt(p []byte, off int64) (n int, err error) {
	return r.BuildDevice.ReadAt(r.ctx, p, off)
}

// alignedReader wraps a block-aligned reader to handle unaligned reads
type alignedReader struct {
	reader    io.ReaderAt
	blockSize int64
}

func (r *alignedReader) ReadAt(p []byte, off int64) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Calculate the aligned start and end offsets
	alignedStart := (off / r.blockSize) * r.blockSize
	alignedEnd := ((off + int64(len(p)) + r.blockSize - 1) / r.blockSize) * r.blockSize

	// Read aligned blocks
	alignedSize := alignedEnd - alignedStart
	alignedBuf := make([]byte, alignedSize)

	readN, err := r.reader.ReadAt(alignedBuf, alignedStart)
	if err != nil && err != io.EOF {
		return 0, err
	}

	// Extract the requested portion
	offsetInBlock := off - alignedStart
	copySize := int64(len(p))
	if offsetInBlock+copySize > int64(readN) {
		copySize = int64(readN) - offsetInBlock
		if copySize < 0 {
			copySize = 0
		}
	}

	copy(p, alignedBuf[offsetInBlock:offsetInBlock+copySize])

	if copySize < int64(len(p)) {
		return int(copySize), io.EOF
	}

	return int(copySize), nil
}

func createFS(ctx context.Context, buildID string) (*ext4.FileSystem, *testutils.Cleaner, error) {
	rootfs, rootfsCleanup, err := testutils.TemplateRootfs(ctx, buildID)
	if err != nil {
		return nil, rootfsCleanup, fmt.Errorf("failed to get template rootfs: %w", err)
	}

	reader := &readerAtWithoutCtx{
		BuildDevice: *rootfs,
		ctx:         ctx,
	}

	// Wrap the reader to handle unaligned reads
	blockSize := int64(rootfs.Header().Metadata.BlockSize)
	alignedReader := &alignedReader{
		reader:    reader,
		blockSize: blockSize,
	}

	sr := io.NewSectionReader(alignedReader, 0, int64(rootfs.Header().Metadata.Size))

	filesystem, err := ext4.NewFS(*sr, nil)
	if err != nil {
		return nil, rootfsCleanup, fmt.Errorf("failed to create ext4 filesystem: %w", err)
	}

	return filesystem, rootfsCleanup, nil
}
