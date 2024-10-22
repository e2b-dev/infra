package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	"golang.org/x/sync/errgroup"
)

type testDevice struct {
	f *os.File
}

func (t *testDevice) BlockSize() int64 {
	return 4096
}

func (t *testDevice) ReadRaw(off int64, size int64) ([]byte, func(), error) {
	b := make([]byte, size)

	n, err := t.f.ReadAt(b, off)

	return b[:n], func() {}, err
}

func (t *testDevice) Size() (int64, error) {
	fmt.Println("getting size")

	fi, err := t.f.Stat()
	if err != nil {
		return 0, err
	}

	return fi.Size(), nil
}

func (t *testDevice) Close() error {
	return t.f.Close()
}

func (t *testDevice) ReadAt(b []byte, off int64) (int, error) {
	fmt.Printf("read at %d, size %d\n", off, len(b))

	return t.f.ReadAt(b, off)
}

func (t *testDevice) WriteAt(b []byte, off int64) (int, error) {
	fmt.Printf("write at %d, size %d\n", off, len(b))

	return t.f.WriteAt(b, off)
}

func (t *testDevice) Sync() error {
	return t.f.Sync()
}

func NewTestDevice(path string) (*testDevice, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	return &testDevice{f: f}, nil
}

func main() {
	fmt.Println("creating nbd device pool")

	pool, err := nbd.NewDevicePool()
	if err != nil {
		fmt.Println("error creating nbd device pool", err)

		return
	}

	fmt.Println("creating test device")

	// dd if=/dev/zero of=test.ext4 bs=4096K count=500
	// mkfs.ext4 test.ext4
	device, err := NewTestDevice(".test/test.ext4")
	if err != nil {
		fmt.Println("error creating test device", err)

		return
	}

	defer device.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := 0; i < 10; i++ {
		td, err := createTestDevice(ctx, pool, device, fmt.Sprintf("test-%d", i))
		if err != nil {
			fmt.Printf("error creating test device %d: %s\n", i, err)
		}

		time.Sleep(2 * time.Second)

		err = td.Close()
		if err != nil {
			fmt.Printf("error closing test device %d: %s\n", i, err)
		}
	}
}

type testNbd struct {
	end       chan struct{}
	nbdClient *nbd.NbdClient
	nbdServer *nbd.NbdServer
}

func (t *testNbd) Close() error {
	var errs []error

	if t.nbdClient != nil {
		errs = append(errs, t.nbdClient.Close())
	}

	if t.nbdServer != nil {
		errs = append(errs, t.nbdServer.Close())
	}

	if t != nil {
		<-t.end
	}

	return errors.Join(errs...)
}

func createTestDevice(ctx context.Context, pool *nbd.DevicePool, device block.Device, id string) (*testNbd, error) {
	fmt.Printf("creating temp file %s\n", id)

	socketPath := fmt.Sprintf("/tmp/nbd-%s.sock", id)
	defer os.Remove(socketPath)

	fmt.Printf("creating nbd %s\n", id)

	n, err := nbd.NewNbdServer(ctx, func() (block.Device, error) {
		return device, nil
	}, socketPath)
	if err != nil {
		fmt.Printf("error creating nbd %s: %s\n", id, err)

		return &testNbd{}, err
	}

	defer n.Close()

	e := errgroup.Group{}

	e.Go(func() error {
		fmt.Printf("starting server %s\n", id)

		serverErr := n.Start()
		if serverErr != nil {
			return fmt.Errorf("error starting server %s: %w", id, serverErr)
		}

		return nil
	})

	nbdClient := n.CreateClient(ctx, pool)

	defer nbdClient.Close()

	e.Go(func() error {
		fmt.Printf("starting client %s\n", id)

		clientErr := nbdClient.Start()
		if clientErr != nil {
			return fmt.Errorf("error starting client %s: %w", id, clientErr)
		}

		return nil
	})

	nbdPath, err := nbdClient.GetPath()
	if err != nil {
		fmt.Printf("error getting path %s: %s\n", id, err)

		return &testNbd{
			nbdServer: n,
		}, err
	}

	fmt.Printf("nbd path %s: %s\n", id, nbdPath)

	end := make(chan struct{})

	go func() {
		end <- struct{}{}

		if waitErr := e.Wait(); waitErr != nil {
			fmt.Printf("error waiting for server and client %s: %s\n", id, waitErr)
		}
	}()

	return &testNbd{
		end:       end,
		nbdClient: nbdClient,
		nbdServer: n,
	}, nil
}
