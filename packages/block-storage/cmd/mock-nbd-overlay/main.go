package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"

	"golang.org/x/sync/errgroup"
)

func main() {
	filePath := flag.String("file", "", "file path")
	flag.Parse()

	pool, err := nbd.NewDevicePool()
	if err != nil {
		log.Fatalf("error creating nbd device pool: %v", err)
	}

	// dd if=/dev/zero of=test.ext4 bs=4096K count=500
	// mkfs.ext4 test.ext4
	device, err := block.NewFileDevice(*filePath)
	if err != nil {
		log.Fatalf("error creating test device: %v", err)
	}

	defer device.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = createTestDevice(ctx, pool, device, "default")
	if err != nil {
		log.Fatalf("error creating test device: %v", err)
	}
}

func createTestDevice(ctx context.Context, pool *nbd.DevicePool, device block.Device, id string) error {
	socketPath := fmt.Sprintf("/tmp/nbd-%s.sock", id)

	err := os.Remove(socketPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing socket path %s: %w", socketPath, err)
	}

	defer os.Remove(socketPath)

	server := nbd.NewServer(
		socketPath,
		func() (block.Device, error) {
			return device, nil
		},
	)

	e, ctx := errgroup.WithContext(ctx)

	e.Go(func() error {
		serverErr := server.Run(ctx)
		if serverErr != nil {
			return fmt.Errorf("error running server %s: %w", id, serverErr)
		}

		return nil
	})

	client, err := server.NewClient(pool)
	if err != nil {
		return fmt.Errorf("error creating client %s: %w", id, err)
	}

	e.Go(func() error {
		clientErr := client.Run(ctx)
		if clientErr != nil {
			return fmt.Errorf("error running client %s: %w", id, clientErr)
		}

		return nil
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-client.Ready:
	}

	fmt.Printf("ndb ready %s\n", client.DevicePath)

	if waitErr := e.Wait(); waitErr != nil {
		return fmt.Errorf("error waiting for server and client %s: %w", id, waitErr)
	}

	return nil
}
