package nbd_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// SlowDevice wraps a ReadonlyDevice and adds a configurable delay to every
// ReadAt call. Used to simulate slow GCS/NFS backends in tests.
type SlowDevice struct {
	inner     block.ReadonlyDevice
	readDelay time.Duration
}

var _ block.ReadonlyDevice = (*SlowDevice)(nil)

func NewSlowDevice(inner block.ReadonlyDevice, readDelay time.Duration) *SlowDevice {
	return &SlowDevice{inner: inner, readDelay: readDelay}
}

func (s *SlowDevice) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	select {
	case <-time.After(s.readDelay):
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	return s.inner.ReadAt(ctx, p, off)
}

func (s *SlowDevice) Size(ctx context.Context) (int64, error) {
	return s.inner.Size(ctx)
}

func (s *SlowDevice) BlockSize() int64 {
	return s.inner.BlockSize()
}

func (s *SlowDevice) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	return s.inner.Slice(ctx, off, length)
}

func (s *SlowDevice) Header() *header.Header {
	return s.inner.Header()
}

func (s *SlowDevice) Close() error {
	return s.inner.Close()
}

// TestSlowBackend_ShortTimeout reproduces the EIO bug: when the
// backend read takes longer than the kernel NBD I/O timeout, the kernel
// declares the connection dead and all I/O returns EIO.
func TestSlowBackend_ShortTimeout(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("the nbd requires root privileges to run")
	}

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)

	const (
		size      = int64(10 * 1024 * 1024)
		blockSize = header.RootfsBlockSize
	)

	emptyDevice, err := testutils.NewZeroDevice(size, blockSize)
	require.NoError(t, err)

	// Backend delays every read by 8 seconds — longer than the kernel timeout below.
	slowDevice := NewSlowDevice(emptyDevice, 8*time.Second)

	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("test-slow-timeout-%s", uuid.New().String()))
	t.Cleanup(func() { os.RemoveAll(cowCachePath) })

	cache, err := block.NewCache(size, blockSize, cowCachePath, false)
	require.NoError(t, err)

	overlay := block.NewOverlay(slowDevice, cache)
	t.Cleanup(func() { overlay.Close() })

	// Kernel I/O timeout of 5s + deadconn 5s = 10s total.
	// The 8s backend delay exceeds the 5s I/O timeout, so the kernel
	// will declare the connection dead and return EIO.
	devicePath, cleanup, err := testutils.GetNBDDevice(
		context.Background(), overlay, featureFlags,
		nbd.WithIOTimeout(5*time.Second),
		nbd.WithDeadconnTimeout(5*time.Second),
	)
	t.Cleanup(func() { cleanup.Run(t.Context(), 30*time.Second) })
	require.NoError(t, err)

	deviceFile, err := os.OpenFile(devicePath, os.O_RDONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { deviceFile.Close() })

	buf := make([]byte, 4096)
	_, err = deviceFile.ReadAt(buf, 0)
	require.Error(t, err, "expected EIO from kernel timeout, but read succeeded")
	t.Logf("got expected error: %v", err)
}

// TestSlowBackend_SufficientTimeout proves the fix: with a kernel timeout
// longer than the backend delay, reads succeed.
func TestSlowBackend_SufficientTimeout(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("the nbd requires root privileges to run")
	}

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)

	const (
		size      = int64(10 * 1024 * 1024)
		blockSize = header.RootfsBlockSize
	)

	emptyDevice, err := testutils.NewZeroDevice(size, blockSize)
	require.NoError(t, err)

	// Backend delays every read by 3 seconds.
	slowDevice := NewSlowDevice(emptyDevice, 3*time.Second)

	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("test-slow-ok-%s", uuid.New().String()))
	t.Cleanup(func() { os.RemoveAll(cowCachePath) })

	cache, err := block.NewCache(size, blockSize, cowCachePath, false)
	require.NoError(t, err)

	overlay := block.NewOverlay(slowDevice, cache)
	t.Cleanup(func() { overlay.Close() })

	// Kernel I/O timeout of 30s — well above the 3s backend delay.
	devicePath, cleanup, err := testutils.GetNBDDevice(
		context.Background(), overlay, featureFlags,
		nbd.WithIOTimeout(30*time.Second),
		nbd.WithDeadconnTimeout(30*time.Second),
	)
	t.Cleanup(func() { cleanup.Run(t.Context(), 30*time.Second) })
	require.NoError(t, err)

	deviceFile, err := os.OpenFile(devicePath, os.O_RDONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { deviceFile.Close() })

	buf := make([]byte, 4096)
	n, err := deviceFile.ReadAt(buf, 0)
	require.NoError(t, err, "read should succeed when timeout > backend delay")
	require.Equal(t, 4096, n)
}

// TestKernel68_NBDDeviceDeath reproduces the kernel 6.8 NBD bug that kills
// the device when sock_sendmsg fails with -EAGAIN during data page send.
//
// The bug path in kernel 6.8 nbd_send_cmd:
//  1. Header (28 bytes) sent successfully to the socket
//  2. Data page send via sock_xmit fails with -EAGAIN (socket buffer full)
//  3. nbd_send_cmd returns -EAGAIN WITHOUT saving partial-send state
//  4. nbd_handle_cmd marks connection dead, requeues, frees the tag
//  5. With 1 connection: no fallback → device dead → EIO
//
// Fixed in kernel 6.14 via NBD_CMD_PARTIAL_SEND.
//
// To trigger: tiny socket buffers (4KB via WithSocketBufferSize) force
// sock_sendmsg to fail with -EAGAIN when sending 128KB WRITE data pages.
//
// On kernel 6.8: writers get EIO, dmesg shows "Send data failed (result -11)".
// On kernel 6.14+: writes succeed (NBD_CMD_PARTIAL_SEND handles the failure).
func TestKernel68_NBDDeviceDeath(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)

	const (
		size      = int64(64 * 1024 * 1024)
		blockSize = header.RootfsBlockSize
	)

	emptyDevice, err := testutils.NewZeroDevice(size, blockSize)
	require.NoError(t, err)

	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("test-k68-death-%s", uuid.New().String()))
	t.Cleanup(func() { os.RemoveAll(cowCachePath) })

	cache, err := block.NewCache(size, blockSize, cowCachePath, false)
	require.NoError(t, err)

	overlay := block.NewOverlay(emptyDevice, cache)
	t.Cleanup(func() { overlay.Close() })

	// Tiny socket buffers (4KB) force sock_sendmsg to fail with -EAGAIN
	// when sending 128KB WRITE data pages. This is the trigger.
	devicePath, cleanup, err := testutils.GetNBDDevice(
		context.Background(), overlay, featureFlags,
		nbd.WithIOTimeout(90*time.Second),
		nbd.WithDeadconnTimeout(30*time.Second),
		nbd.WithSocketBufferSize(4096),
	)
	t.Cleanup(func() { cleanup.Run(t.Context(), 120*time.Second) })
	require.NoError(t, err)

	devName := string(devicePath)[len("/dev/"):]
	t.Logf("NBD device: %s", devName)

	deviceFile, err := os.OpenFile(string(devicePath), os.O_RDWR|os.O_SYNC, 0)
	require.NoError(t, err)
	t.Cleanup(func() { deviceFile.Close() })

	exec.Command("dmesg", "-C").Run()

	// Issue concurrent 128KB writes. With 4KB socket buffers, the kernel
	// can't send 128KB of data pages without blocking. Under contention
	// from multiple writers, sock_sendmsg returns -EAGAIN.
	var wg sync.WaitGroup
	var okCount, eioCount, otherCount int64

	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			buf := make([]byte, 128*1024)
			for j := range buf {
				buf[j] = byte(id)
			}
			for attempt := range 20 {
				off := int64(id*128*1024+attempt*128*1024) % (size - 128*1024)
				off = (off / 4096) * 4096
				_, err := deviceFile.WriteAt(buf, off)
				if err != nil {
					if strings.Contains(err.Error(), "input/output error") {
						eioCount++
					} else {
						otherCount++
						t.Logf("writer %d: %v", id, err)
					}
					return
				}
				okCount++
			}
		}(i)
	}

	wg.Wait()

	t.Logf("writes OK: %d, EIO: %d, other errors: %d", okCount, eioCount, otherCount)

	// Check dmesg.
	dmesgCmd := exec.Command("dmesg", "--notime")
	out, _ := dmesgCmd.Output()
	kmsg := string(out)

	hasSendFailed := strings.Contains(kmsg, "Send data failed")
	hasRequeueing := strings.Contains(kmsg, "Request send failed")
	hasDeadConn := strings.Contains(kmsg, "Dead connection")

	if hasSendFailed {
		t.Logf("dmesg: 'Send data failed' — kernel hit -EAGAIN on data page send")
	}
	if hasRequeueing {
		t.Logf("dmesg: 'Request send failed, requeueing' — entered buggy requeue path")
	}
	if hasDeadConn {
		t.Logf("dmesg: 'Dead connection' — device killed (1 connection, no fallback)")
	}

	if hasSendFailed && hasDeadConn {
		if eioCount > 0 {
			t.Logf("BUG CONFIRMED: -EAGAIN on data send → device death → EIO")
			t.Logf("This kernel has the NBD partial-send bug (fixed in 6.14)")
		}
	} else if eioCount == 0 && okCount > 0 {
		t.Logf("All writes succeeded — kernel is fixed (6.14+) or buffer pressure insufficient")
	}
}
