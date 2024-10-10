package nbd

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
)

type NbdServer struct {
	socketPath      string
	getStorage      func() (block.Device, error)
	closeOnce       func() error
	ready           chan error
	EnsureListening func() error
}

func (n *NbdServer) Close() error {
	listeningErr := n.EnsureListening()
	if listeningErr != nil {
		return fmt.Errorf("error ensuring server is listening: %w", listeningErr)
	}

	closeErr := n.closeOnce()
	if closeErr != nil {
		return fmt.Errorf("error closing server: %w", closeErr)
	}

	return nil
}

func NewNbdServer(
	ctx context.Context,
	getStorage func() (block.Device, error),
	socketPath string,
) (*NbdServer, error) {
	ready := make(chan error)

	return &NbdServer{
		getStorage: getStorage,
		socketPath: socketPath,
		ready:      ready,
		EnsureListening: sync.OnceValue(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-ready:
				return err
			}
		}),
	}, nil
}

func (n *NbdServer) Start() error {
	l, err := net.Listen("unix", n.socketPath)
	if err != nil {
		errMsg := fmt.Errorf("failed to listen on socket: %w", err)

		n.ready <- errMsg

		return errMsg
	}

	n.closeOnce = sync.OnceValue(func() error {
		return l.Close()
	})

	defer n.closeOnce()

	n.ready <- nil

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}

		go func() {
			defer func() {
				_ = conn.Close()

				if err := recover(); err != nil {
					fmt.Printf("Client disconnected with error: %v", err)
				}
			}()

			storage, err := n.getStorage()
			if err != nil {
				fmt.Printf("Could not get storage: %v", err)

				return
			}

			err = server.Handle(
				conn,
				[]*server.Export{
					{
						Name:        "default",
						Description: "The default export",
						Backend:     storage,
					},
				},
				&server.Options{
					ReadOnly:           false,
					MinimumBlockSize:   uint32(1),
					PreferredBlockSize: uint32(4096),
					MaximumBlockSize:   uint32(0xffffffff),
					SupportsMultiConn:  true,
				})
			if err != nil {
				fmt.Printf("Client disconnected with error: %v", err)

				return
			}
		}()
	}
}

func (n *NbdServer) CreateClient(pool *NbdDevicePool) *NbdClient {
	ready := make(chan error)

	return &NbdClient{
		ready:                 ready,
		socketPath:            n.socketPath,
		pool:                  pool,
		ensureServerListening: n.EnsureListening,
		getPath: sync.OnceValue(func() (string, error) {
			return n.GetPath(ctx)
		}),
	}
}

type NbdClient struct {
	ready                 chan error
	socketPath            string
	path                  string
	f                     *os.File
	pool                  *NbdDevicePool
	ensureServerListening func() error
}

// This method can only be called once.
func (n *NbdClient) GetPath(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-n.ready:
		if err != nil {
			return "", err
		}
	}

	return n.path, nil
}

func (n *NbdClient) Close() error {
	<-n.ready
	// TODO: Make this so it can be called multiple times (getPath)

	if n.f != nil {
		err := client.Disconnect(n.f)
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *NbdClient) Start(ctx context.Context) error {
	var err error

	defer func() {
		if err != nil {
			n.ready <- err
		}

		close(n.ready)
	}()

	nbdPath, err := n.pool.GetDevice(ctx)
	if err != nil {
		return fmt.Errorf("failed to get nbd device: %w", err)
	}

	defer func() {
		releaseErr := n.pool.ReleaseDevice(nbdPath)
		if releaseErr != nil {
			fmt.Fprintf(os.Stderr, "failed to release device: %s, %v\n", nbdPath, releaseErr)
		}
	}()

	n.path = nbdPath

	err = n.ensureServerListening()
	if err != nil {
		return fmt.Errorf("failed to ensure server is listening: %w", err)
	}

	conn, err := net.Dial("unix", n.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	defer conn.Close()

	f, err := os.OpenFile(nbdPath, os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	defer f.Close()

	n.f = f

	err = client.Connect(conn, f, &client.Options{
		ExportName: "default",
		BlockSize:  uint32(4096),
		OnConnected: func() {
			n.ready <- nil
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	return nil
}
