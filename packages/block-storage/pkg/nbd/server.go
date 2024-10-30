package nbd

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/pojntfx/go-nbd/pkg/server"
)

type Server struct {
	getStorage func() (block.Device, error)
	ready      chan struct{}
	socketPath string
}

func NewServer(socketPath string, getStorage func() (block.Device, error)) *Server {
	return &Server{
		getStorage: getStorage,
		socketPath: socketPath,
		ready:      make(chan struct{}),
	}
}

func (n *Server) Run(ctx context.Context) error {
	defer close(n.ready)

	var lc net.ListenConfig

	l, err := lc.Listen(ctx, "unix", n.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	go func() {
		<-ctx.Done()

		closeErr := l.Close()
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "failed to close listener: %s\n", closeErr.Error())
		}
	}()

	n.ready <- struct{}{}

	for {
		conn, acceptErr := l.Accept()
		if acceptErr != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				fmt.Fprintf(os.Stderr, "failed to accept connection: %s\n", acceptErr.Error())

				continue
			}
		}

		go func() {
			defer func() {
				_ = conn.Close()

				if err := recover(); err != nil {
					fmt.Fprintf(os.Stderr, "recovering from NBD server panic: %v\n", err)
				}
				// TODO: We should close the server on this panic.
			}()

			storage, err := n.getStorage()
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not get storage: %s\n", err.Error())

				return
			}

			blockSize := uint32(storage.BlockSize())

			err = server.Handle(
				conn,
				[]*server.Export{
					{
						Backend: storage,
						Name:    "default",
					},
				},
				&server.Options{
					ReadOnly:           false,
					MinimumBlockSize:   blockSize,
					PreferredBlockSize: blockSize,
					MaximumBlockSize:   blockSize,
					SupportsMultiConn:  true,
				})
			if err != nil {
				fmt.Fprintf(os.Stderr, "client disconnected with error: %s\n", err.Error())

				return
			}
		}()
	}
}
