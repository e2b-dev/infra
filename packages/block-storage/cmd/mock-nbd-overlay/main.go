package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"

	"golang.org/x/sync/errgroup"
)

func main() {
	filePath := flag.String("file", "", "file path")
	flag.Parse()

	fmt.Println("creating nbd device pool")

	pool, err := nbd.NewDevicePool()
	if err != nil {
		fmt.Println("error creating nbd device pool", err)

		return
	}

	fmt.Println("creating test device")

	// dd if=/dev/zero of=test.ext4 bs=4096K count=500
	// mkfs.ext4 test.ext4
	device, err := block.NewFileDevice(*filePath)
	if err != nil {
		fmt.Println("error creating test device", err)

		return
	}

	defer device.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = createTestDevice(ctx, pool, device, "default")
	if err != nil {
		fmt.Printf("error creating test device: %s\n", err)
	}
}

func createTestDevice(ctx context.Context, pool *nbd.DevicePool, device block.Device, id string) error {
	fmt.Printf("creating temp file %s\n", id)

	socketPath := fmt.Sprintf("/tmp/nbd-%s.sock", id)

	err := os.Remove(socketPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing socket path %s: %w", socketPath, err)
	}

	defer os.Remove(socketPath)

	fmt.Printf("creating nbd %s\n", id)

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
			fmt.Printf("error running server %s: %v\n", id, serverErr)
		} else {
			fmt.Printf("server closed %s\n", id)
		}

		return nil
	})

	client, err := server.NewClient(pool)
	if err != nil {
		return fmt.Errorf("error creating client %s: %v", id, err)
	}

	e.Go(func() error {
		clientErr := client.Run(ctx)
		if clientErr != nil {
			fmt.Printf("error running client %s: %v\n", id, clientErr)
		} else {
			fmt.Printf("client closed %s\n", id)
		}

		return nil
	})

	fmt.Printf("waiting for client ready %s\n", id)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-client.Ready:
		fmt.Printf("client ready")
	}

	fmt.Printf("nbd path %s: %s\n", id, client.DevicePath)

	if waitErr := e.Wait(); waitErr != nil {
		return fmt.Errorf("error waiting for server and client %s: %w", id, waitErr)
	}

	return nil
}
