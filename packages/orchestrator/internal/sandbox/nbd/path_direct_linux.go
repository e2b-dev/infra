//go:build linux
// +build linux

package nbd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Merovius/nbd/nbdnl"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	connections    = 4
	connectTimeout = 30 * time.Second

	// disconnectTimeout should not be necessary if the disconnect is reliable
	disconnectTimeout = 30 * time.Second
)

type DirectPathMount struct {
	tracer   trace.Tracer
	ctx      context.Context
	cancelfn context.CancelFunc

	devicePool *DevicePool

	Backend     block.Device
	deviceIndex uint32
	blockSize   uint64

	dispatchers []*Dispatch
	socksClient []*os.File
	socksServer []io.Closer

	handlersWg sync.WaitGroup
}

func NewDirectPathMount(tracer trace.Tracer, b block.Device, devicePool *DevicePool) *DirectPathMount {
	ctx, cancelfn := context.WithCancel(context.Background())

	return &DirectPathMount{
		tracer:      tracer,
		Backend:     b,
		ctx:         ctx,
		cancelfn:    cancelfn,
		blockSize:   4096,
		devicePool:  devicePool,
		socksClient: make([]*os.File, 0),
		socksServer: make([]io.Closer, 0),
	}
}

func (d *DirectPathMount) Open(ctx context.Context) (deviceIndex uint32, err error) {
	size, err := d.Backend.Size()
	if err != nil {
		return 0, err
	}

	d.deviceIndex, err = d.devicePool.GetDevice(ctx)
	if err != nil {
		return 0, err
	}

	for {
		d.socksClient = make([]*os.File, 0)
		d.socksServer = make([]io.Closer, 0)
		d.dispatchers = make([]*Dispatch, 0)

		for i := 0; i < connections; i++ {
			// Create the socket pairs
			sockPair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
			if err != nil {
				return 0, err
			}

			client := os.NewFile(uintptr(sockPair[0]), "client")
			server := os.NewFile(uintptr(sockPair[1]), "server")
			serverc, err := net.FileConn(server)
			if err != nil {
				return 0, err
			}
			server.Close()

			dispatch := NewDispatch(d.ctx, serverc, d.Backend)
			// Start reading commands on the socket and dispatching them to our provider
			d.handlersWg.Add(1)
			go func() {
				defer d.handlersWg.Done()

				handleErr := dispatch.Handle()
				// The error is expected to happen if the nbd (socket connection) is closed
				zap.L().Info("closing handler for NBD commands",
					zap.Error(handleErr),
					zap.Uint32("device_index", d.deviceIndex),
					zap.Int("socket_index", i),
				)
			}()

			d.socksServer = append(d.socksServer, serverc)
			d.socksClient = append(d.socksClient, client)
			d.dispatchers = append(d.dispatchers, dispatch)
		}

		var opts []nbdnl.ConnectOption
		opts = append(opts, nbdnl.WithBlockSize(d.blockSize))
		opts = append(opts, nbdnl.WithTimeout(connectTimeout))
		opts = append(opts, nbdnl.WithDeadconnTimeout(connectTimeout))

		serverFlags := nbdnl.FlagHasFlags | nbdnl.FlagCanMulticonn

		idx, err := nbdnl.Connect(d.deviceIndex, d.socksClient, uint64(size), 0, serverFlags, opts...)
		if err == nil {
			// The idx should be the same as d.deviceIndex, because we are connecting to it,
			// but we will use the one returned by nbdnl
			d.deviceIndex = idx

			break
		}

		zap.L().Error("error opening NBD, retrying", zap.Error(err), zap.Uint32("deviceIndex", d.deviceIndex))

		// Sometimes (rare), there seems to be a BADF error here. Lets just retry for now...
		// Close things down and try again...
		for _, sock := range d.socksClient {
			sock.Close()
		}
		for _, sock := range d.socksServer {
			sock.Close()
		}

		if strings.Contains(err.Error(), "invalid argument") {
			return 0, err
		}

		select {
		case <-d.ctx.Done():
			return 0, errors.Join(err, d.ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}

	// Wait until it's connected...
	for {
		select {
		case <-d.ctx.Done():
			return 0, d.ctx.Err()
		default:
		}

		s, err := nbdnl.Status(d.deviceIndex)
		if err == nil && s.Connected {
			break
		}

		time.Sleep(100 * time.Nanosecond)
	}

	return d.deviceIndex, nil
}

func (d *DirectPathMount) Close(ctx context.Context) error {
	childCtx, childSpan := d.tracer.Start(ctx, "direct-path-mount-close")
	defer childSpan.End()

	// First cancel the context, which will stop waiting on pending readAt/writeAt...
	telemetry.ReportEvent(childCtx, "canceling context")
	d.cancelfn()

	// Close all server socket pairs...
	telemetry.ReportEvent(childCtx, "closing socket pairs server")
	for _, v := range d.socksServer {
		err := v.Close()
		if err != nil {
			return err
		}
	}

	// Now wait until the handlers return
	telemetry.ReportEvent(childCtx, "await handlers return")
	d.handlersWg.Wait()

	// Now wait for any pending responses to be sent
	telemetry.ReportEvent(childCtx, "waiting for pending responses")
	for _, d := range d.dispatchers {
		d.Drain()
	}

	// Now ask to disconnect
	telemetry.ReportEvent(childCtx, "disconnecting NBD")
	err := nbdnl.Disconnect(d.deviceIndex)
	if err != nil {
		return err
	}

	// Wait until it's completely disconnected...
	telemetry.ReportEvent(childCtx, "waiting for complete disconnection")
	ctxTimeout, cancel := context.WithTimeout(childCtx, disconnectTimeout)
	defer cancel()
	for {
		select {
		case <-ctxTimeout.Done():
			return ctxTimeout.Err()
		default:
		}

		s, err := nbdnl.Status(d.deviceIndex)
		if err == nil && !s.Connected {
			break
		}
		time.Sleep(100 * time.Nanosecond)
	}

	// Close all client socket pairs...
	telemetry.ReportEvent(childCtx, "closing socket pairs client")
	for _, v := range d.socksClient {
		err := v.Close()
		if err != nil {
			return err
		}
	}

	// Release the device back to the pool, retry if it is in use
	telemetry.ReportEvent(childCtx, "releasing device to the pool")
	attempt := 0
	for {
		attempt++
		err := d.devicePool.ReleaseDevice(d.deviceIndex)
		if errors.Is(err, ErrDeviceInUse{}) {
			if attempt%100 == 0 {
				zap.L().Error("error releasing overlay device", zap.Int("attempt", attempt), zap.Error(err))
			}

			time.Sleep(500 * time.Millisecond)

			continue
		}

		if err != nil {
			return fmt.Errorf("error releasing overlay device: %w", err)
		}

		break
	}

	return nil
}
