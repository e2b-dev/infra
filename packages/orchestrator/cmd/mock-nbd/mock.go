package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/signal"

	"github.com/pojntfx/go-nbd/pkg/backend"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
)

const blockSize = 4096

type DeviceWithClose struct {
	backend.Backend
}

func (d *DeviceWithClose) Close() error {
	return nil
}

func (d *DeviceWithClose) Slice(offset, length int64) ([]byte, error) {
	b := make([]byte, length)

	_, err := d.Backend.ReadAt(b, offset)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func main() {
	data := make([]byte, blockSize*8)
	rand.Read(data)

	device := &DeviceWithClose{
		Backend: backend.NewMemoryBackend(data),
	}

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

func MockNbd(ctx context.Context, device *DeviceWithClose, index int) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	size, err := device.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get size: %w", err)
	}

	deviceIndex, err := nbd.MustGetDevicePool().GetDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	var mnt *nbd.DirectPathMount

	defer func() {
		counter := 0

		for {
			counter += 1
			err = nbd.MustGetDevicePool().ReleaseDevice(deviceIndex)
			if err != nil {
				if counter%10 == 0 {
					fmt.Printf("[%d] failed to release device: %v\n", index, err)
				}

				if mnt != nil {
					mnt.Close()
				}

				continue
			}

			fmt.Printf("[%d] released device: %d\n", index, deviceIndex)

			return
		}
	}()

	mnt = nbd.NewDirectPathMount(device)

	go func() {
		<-ctx.Done()

		mnt.Close()
	}()

	_, err = mnt.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}

	data := make([]byte, size)
	_, err = mnt.Backend.ReadAt(data, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	fmt.Printf("[%d] Read %d bytes from nbd\n", index, len(data))

	cancel()

	return data, nil
}
