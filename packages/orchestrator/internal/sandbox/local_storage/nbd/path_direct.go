package nbd

import (
	"context"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/Merovius/nbd/nbdnl"
	"github.com/pojntfx/go-nbd/pkg/backend"
)

type DirectPathMount struct {
	Backend     backend.Backend
	ctx         context.Context
	dispatcher  *Dispatch
	conn        net.Conn
	deviceIndex uint32
	blockSize   uint64
	cancelfn    context.CancelFunc
}

func NewDirectPathMount(
	b backend.Backend,
	deviceIndex uint32,
) *DirectPathMount {
	ctx, cancelfn := context.WithCancel(context.Background())
	return &DirectPathMount{
		Backend:     b,
		ctx:         ctx,
		cancelfn:    cancelfn,
		deviceIndex: deviceIndex,
		blockSize:   4096,
	}
}

func (d *DirectPathMount) Open() error {
	size, err := d.Backend.Size()
	if err != nil {
		return err
	}

	for {
		// Create the socket pairs
		sockPair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			return err
		}

		client := os.NewFile(uintptr(sockPair[0]), "client")
		server := os.NewFile(uintptr(sockPair[1]), "server")
		d.conn, err = net.FileConn(server)
		if err != nil {
			return err
		}
		server.Close()

		dis := NewDispatch(d.ctx, d.conn, d.Backend)
		// Start reading commands on the socket and dispatching them to our provider
		go func() {
			handleErr := dis.Handle()
			if handleErr != nil {
				log.Printf("Error handling NBD commands: %v", handleErr)
			}
		}()
		d.dispatcher = dis

		var opts []nbdnl.ConnectOption
		opts = append(opts, nbdnl.WithBlockSize(d.blockSize))
		opts = append(opts, nbdnl.WithTimeout(5*time.Second))
		opts = append(opts, nbdnl.WithDeadconnTimeout(5*time.Second))

		serverFlags := nbdnl.FlagHasFlags | nbdnl.FlagCanMulticonn

		idx, err := nbdnl.Connect(d.deviceIndex, []*os.File{client}, uint64(size), 0, serverFlags, opts...)
		if err == nil {
			d.deviceIndex = idx
			break
		}

		// Sometimes (rare), there seems to be a BADF error here. Lets just retry for now...
		// Close things down and try again...
		client.Close()

		if strings.Contains(err.Error(), "invalid argument") {
			return err
		}

		time.Sleep(50 * time.Millisecond)
	}

	// Wait until it's connected...
	for {
		s, err := nbdnl.Status(uint32(d.deviceIndex))
		if err == nil && s.Connected {
			break
		}
		time.Sleep(100 * time.Nanosecond)
	}

	return nil
}

func (d *DirectPathMount) Close() error {
	// First cancel the context, which will stop waiting on pending readAt/writeAt...
	d.ctx.Done()

	// Now wait for any pending responses to be sent
	d.dispatcher.Wait()

	// Now ask to disconnect
	err := nbdnl.Disconnect(uint32(d.deviceIndex))
	if err != nil {
		return err
	}

	// Close all the socket pairs...
	err = d.conn.Close()
	if err != nil {
		return err
	}

	// Wait until it's completely disconnected...
	for {
		s, err := nbdnl.Status(uint32(d.deviceIndex))
		if err == nil && !s.Connected {
			break
		}
		time.Sleep(100 * time.Nanosecond)
	}

	return nil
}

// TODO: remove, only for mock
func (d *DirectPathMount) ReadAt(data []byte, offset int64) (int, error) {
	return d.Backend.ReadAt(data, offset)
}

func (d *DirectPathMount) Sync() error {
	return nil
}
