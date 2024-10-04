package block_storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"

	"github.com/stretchr/testify/require"
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

func (t *testDevice) Size() int64 {
	fi, err := t.f.Stat()
	if err != nil {
		return 0
	}

	return fi.Size()
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
		return nil, err
	}

	return &testDevice{f: f}, nil
}

func TestStorageOverlayNBD(t *testing.T) {
	pool, err := nbd.NewNbdDevicePool()
	require.NoError(t, err)

	// dd if=/dev/zero of=test.ext4 bs=4096K count=500
	// mkfs.ext4 test.ext4
	device, err := NewTestDevice("./../.test/test.ext4")
	require.NoError(t, err)

	defer device.Close()

	ctx := context.Background()

	n, err := nbd.NewNbd(ctx, device, pool)
	require.NoError(t, err)

	go n.Run()

	defer n.Close()

	fmt.Printf("nbd path: %s\n", n.Path)

	time.Sleep(60 * time.Second)
}
