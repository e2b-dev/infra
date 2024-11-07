package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"

	nbd "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage/custom"
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

		log.Printf("[%d] starting mock nbd server\n", i)

		readData, err := MockNbd(ctx, device, i)
		if err != nil {
			log.Printf("[%d] failed to mock nbd: %v\n", i, err)

			return
		}

		if !bytes.Equal(data, readData) {
			log.Printf("[%d] data mismatch\n", i)

			return
		}
	}
}

func MockNbd(ctx context.Context, device backend.Backend, index int) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	socketPath := fmt.Sprintf("/tmp/nbd-%d.sock", index)

	err := os.Remove(socketPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("error removing socket path %s: %w", socketPath, err)
	}
	defer os.Remove(socketPath)

	mnt, err := nbd.NewMount(ctx, socketPath, device, strconv.FormatUint(uint64(index), 10))

	go func() {
		runErr := mnt.Run()
		if runErr != nil {
			log.Printf("[%d] failed to run nbd: %v\n", index, runErr)
		}
	}()

	go func() {
		<-ctx.Done()

		err := mnt.Close()
		if err != nil {
			log.Printf("[%d] failed to close nbd: %v\n", index, err)
		}
	}()

	devicePath, err := mnt.Path()
	if err != nil {
		return nil, fmt.Errorf("failed to get path: %w", err)
	}

	deviceFile, err := os.Open(devicePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open device: %w", err)
	}
	defer deviceFile.Close()

	size, err := device.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get size: %w", err)
	}

	data := make([]byte, size)
	_, err = deviceFile.ReadAt(data, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	log.Printf("[%d] Read %d bytes from nbd\n", index, len(data))

	return data, nil
}
