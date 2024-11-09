package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage/nbd"
	"github.com/pojntfx/go-nbd/pkg/backend"
)

const blockSize = 4096

func main() {
	data := make([]byte, blockSize*1)
	rand.Read(data)

	device := backend.NewMemoryBackend(data)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	go func() {
		<-done

		cancel()
	}()

	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		fmt.Printf("----------------------------------------\n")
		fmt.Printf("[%d] starting mock nbd server\n", i)

		readData, err := MockNbd(ctx, device, i)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d] failed to mock nbd: %v\n", i, err)

			return
		}

		if !bytes.Equal(data, readData) {
			fmt.Fprintf(os.Stderr, "[%d] data mismatch\n", i)

			return
		}
	}
}

func MockNbd(ctx context.Context, device backend.Backend, index int) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	size, err := device.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get size: %w", err)
	}

	devicePath, err := nbd.Pool.GetDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}
	var mnt *nbd.DirectPathMount
	defer func() {
		counter := 0
		for {
			counter += 1
			err = nbd.Pool.ReleaseDevice(devicePath)
			if err != nil {
				if counter%10 == 0 {
					fmt.Printf("[%d] failed to release device: %v\n", index, err)
				}

				if mnt != nil {
					mnt.Close()
				}

				continue
			}

			fmt.Printf("[%d] released device: %s\n", index, devicePath)

			return
		}
	}()

	mnt = nbd.NewDirectPathMount(device, devicePath, nil, nil)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		if err := mnt.Wait(); err != nil {
			fmt.Printf("[%d] failed to wait for nbd: %v\n", index, err)
		}
	}()

	go func() {
		<-ctx.Done()

		mnt.Close()
	}()

	err = mnt.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}

	data := make([]byte, size)
	_, err = mnt.ReadAt(data, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	fmt.Printf("[%d] Read %d bytes from nbd\n", index, len(data))

	cancel()

	wg.Wait()

	return data, nil
}
