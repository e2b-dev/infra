package nbd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
)

type NbdServer struct {
	getStorage      func() (block.Device, error)
	closeOnce       func() error
	ready           chan error
	ensureListening func() error
	socketPath      string
}

func (n *NbdServer) Close() error {
	listeningErr := n.ensureListening()
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
		ensureListening: sync.OnceValue(func() error {
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
	defer close(n.ready)

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
			// TODO: Fix failure to accept after close
			// fmt.Fprintf(os.Stderr, "Failed to accept connection: %v\n", err)

			continue
		}

		go func() {
			defer func() {
				_ = conn.Close()

				if err := recover(); err != nil {
					fmt.Fprintf(os.Stderr, "Client disconnected with error: %v\n", err)
				}
			}()

			// TODO: Use the remote/local address to identify the client so we can save the cache storage to specific file.
			storage, err := n.getStorage()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Could not get storage: %v\n", err)

				return
			}

			blockSize := uint32(storage.BlockSize())

			err = server.Handle(
				conn,
				[]*server.Export{
					{
						Name:        "default",
						Description: "",
						Backend:     storage,
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
				fmt.Fprintf(os.Stderr, "Client disconnected with error: %v\n", err)

				return
			}
		}()
	}
}

type NbdClient struct {
	ready                 chan clientResult
	f                     *os.File
	pool                  *NbdDevicePool
	ensureServerListening func() error
	GetPath               func() (string, error)
	ctx                   context.Context
	socketPath            string
	path                  string
}

type clientResult struct {
	err  error
	path string
}

func (n *NbdServer) CreateClient(ctx context.Context, pool *NbdDevicePool) *NbdClient {
	ready := make(chan clientResult)

	return &NbdClient{
		ready:                 ready,
		socketPath:            n.socketPath,
		pool:                  pool,
		ctx:                   ctx,
		ensureServerListening: n.ensureListening,
		GetPath: sync.OnceValues(func() (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case result := <-ready:
				if result.err != nil {
					return "", result.err
				}

				return result.path, nil
			}
		}),
	}
}

func (n *NbdClient) Close() error {
	var errs []error

	if n.f != nil {
		err := client.Disconnect(n.f)
		if err != nil {
			errs = append(errs, err)
		}

		err = n.f.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (n *NbdClient) Start() error {
	defer close(n.ready)

	var err error

	defer func() {
		if err != nil {
			n.ready <- clientResult{err: err}
		}
	}()

	nbdPath, err := n.pool.GetDevice(n.ctx)
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

	n.f = f

	err = client.Connect(conn, f, &client.Options{
		ExportName: "default",
		// 0 means the server will choose the preferred block size
		BlockSize: uint32(0),
		OnConnected: func() {
			n.ready <- clientResult{path: n.path}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	return nil
}
