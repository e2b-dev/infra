package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/google/uuid"
)

const blockSize = 4096

type DeviceWithClose struct {
	*build.File
	size int64
}

func (d *DeviceWithClose) Close() error {
	return nil
}

func (d *DeviceWithClose) Size() (int64, error) {
	return d.size, nil
}

func (d *DeviceWithClose) ReadAt(p []byte, off int64) (int, error) {
	fmt.Printf("ReadAt %d bytes at offset %d\n", len(p), off)

	return d.File.ReadAt(p, off)
}

func main() {
	buildId := flag.String("build", "", "build id")

	flag.Parse()

	template := storage.NewTemplateFiles(
		"",
		*buildId,
		"",
		"",
		false,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

	storagePath := template.StorageRootfsHeaderPath()
	obj := gcs.NewObject(ctx, gcs.TemplateBucket, storagePath)

	h, err := header.Deserialize(obj)
	if err != nil {
		id, err := uuid.Parse(*buildId)
		if err != nil {
			log.Fatalf("failed to parse build id: %s", err)
		}

		object := gcs.NewObject(ctx, gcs.TemplateBucket, *buildId+"/"+string(build.Rootfs))

		size, err := object.Size()
		if err != nil {
			log.Fatalf("failed to get object size: %s", err)
		}

		h = header.NewHeader(&header.Metadata{
			BuildId:     id,
			BaseBuildId: id,
			Size:        uint64(size),
			Version:     1,
			BlockSize:   uint64(blockSize),
			Generation:  1,
		}, nil)
	}

	store, err := build.NewDiffStore(gcs.TemplateBucket, ctx)
	if err != nil {
		log.Fatalf("failed to create diff store: %s", err)
	}

	rootfs := build.NewFile(h, store, build.Rootfs)

	random := uuid.New().String()
	cachePath := filepath.Join(os.TempDir(), fmt.Sprintf("rootfs.cache-%s", random))

	cache, err := block.NewCache(int64(h.Metadata.Size), blockSize, cachePath, false)
	if err != nil {
		log.Fatalf("failed to create cache: %s", err)
	}

	fmt.Printf("cachePath: %+v\n", h.Metadata)

	overlay := block.NewOverlay(&DeviceWithClose{rootfs, int64(h.Metadata.Size)}, cache, blockSize)
	defer overlay.Close()

	mnt := nbd.NewDirectPathMount(overlay)

	go func() {
		<-ctx.Done()

		fmt.Println("Closing mnt")

		mnt.Close()
	}()

	mntIndex, err := mnt.Open(ctx)
	if err != nil {
		log.Fatalf("failed to open: %s", err)
	}

	devicePath := nbd.GetDevicePath(mntIndex)

	fmt.Printf("Mounted rootfs at %s\n", devicePath)

	<-ctx.Done()
}
