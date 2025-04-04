//go:build linux
// +build linux

package nbd

import (
	"context"
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
	connections = 4
	timeout     = 30 * time.Second
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
	socks       []io.Closer

	handlersWg sync.WaitGroup
}

func NewDirectPathMount(tracer trace.Tracer, b block.Device, devicePool *DevicePool) *DirectPathMount {
	ctx, cancelfn := context.WithCancel(context.Background())

	return &DirectPathMount{
		tracer:     tracer,
		Backend:    b,
		ctx:        ctx,
		cancelfn:   cancelfn,
		blockSize:  4096,
		devicePool: devicePool,
		socks:      make([]io.Closer, 0),
	}
}

func (d *DirectPathMount) Open(ctx context.Context) (deviceIndex uint32, err error) {
	size, err := d.Backend.Size()
	if err != nil {
		return 0, err
	}

	// TODO: Do we need retry getting the device or it is enough to retry the dispatchers?
	d.deviceIndex, err = d.devicePool.GetDevice(ctx)
	if err != nil {
		return 0, err
	}

	defer func() {
		if err != nil {
			err := d.devicePool.ReleaseDevice(d.deviceIndex)
			if err != nil {
				zap.L().Error("error releasing device in direct path mount", zap.Error(err))
			}

			d.deviceIndex = 0
		}
	}()

	for {
		socks := make([]*os.File, 0)
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
				if handleErr != nil {
					zap.L().Error("error handling NBD commands", zap.Error(handleErr))
				}
			}()

			d.socks = append(d.socks, serverc)
			socks = append(socks, client)
			d.dispatchers = append(d.dispatchers, dispatch)
		}

		var opts []nbdnl.ConnectOption
		opts = append(opts, nbdnl.WithBlockSize(d.blockSize))
		opts = append(opts, nbdnl.WithTimeout(timeout))
		opts = append(opts, nbdnl.WithDeadconnTimeout(timeout))

		serverFlags := nbdnl.FlagHasFlags | nbdnl.FlagCanMulticonn

		idx, err := nbdnl.Connect(d.deviceIndex, socks, uint64(size), 0, serverFlags, opts...)
		if err == nil {
			d.deviceIndex = idx

			break
		}

		// Sometimes (rare), there seems to be a BADF error here. Lets just retry for now...
		// Close things down and try again...
		for _, sock := range socks {
			sock.Close()
		}

		if strings.Contains(err.Error(), "invalid argument") {
			return 0, err
		}

		time.Sleep(50 * time.Millisecond)
	}

	// Wait until it's connected...
	for {
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

	// Close all the socket pairs...
	telemetry.ReportEvent(childCtx, "closing socket pairs")
	for _, v := range d.socks {
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
		d.Wait()
	}

	// Now ask to disconnect
	telemetry.ReportEvent(childCtx, "disconnecting NBD")
	err := nbdnl.Disconnect(d.deviceIndex)
	if err != nil {
		return err
	}

	// Wait until it's completely disconnected...
	telemetry.ReportEvent(childCtx, "waiting for complete disconnection")
	ctxTimeout, cancel := context.WithTimeout(childCtx, timeout)
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

	return nil
}
