package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage/nbd"
	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/r3map/pkg/mount"
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
	defer func() {
		for {
			err = nbd.Pool.ReleaseDevice(devicePath)
			if err != nil {
				fmt.Printf("[%d] failed to release device: %v\n", index, err)

				continue
			}

			fmt.Printf("[%d] released device: %s\n", index, devicePath)

			return
		}
	}()

	deviceFile, err := os.Open(devicePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open device: %w", err)
	}

	mnt := mount.NewDirectPathMount(device, deviceFile, nil, nil)

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

		mntCloseErr := mnt.Close()

		deviceCloseErr := deviceFile.Close()

		err = errors.Join(mntCloseErr, deviceCloseErr)
		if err != nil {
			fmt.Printf("[%d] Closed nbd errors: %v\n", index, err)
		} else {
			fmt.Printf("[%d] Closed nbd\n", index)
		}
	}()

	defer mnt.Close()
	err = mnt.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}

	fmt.Printf("[%d] Reading from nbd\n", index)

	data := make([]byte, size)
	_, err = deviceFile.ReadAt(data, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	fmt.Printf("[%d] Read %d bytes from nbd\n", index, len(data))

	cancel()

	wg.Wait()

	return data, nil
}
