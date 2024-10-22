package nbd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

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
	closed          atomic.Bool
}

func (n *NbdServer) Close() error {
	var errs []error

	listeningErr := n.ensureListening()
	if listeningErr != nil {
		errs = append(errs, fmt.Errorf("error ensuring server is listening: %w", listeningErr))
	}

	closeErr := n.closeOnce()
	if closeErr != nil {
		errs = append(errs, fmt.Errorf("error closing server: %w", closeErr))
	}

	return errors.Join(errs...)
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
	defer func() {
		n.ready <- nil
		close(n.ready)
	}()

	l, err := net.Listen("unix", n.socketPath)
	if err != nil {
		errMsg := fmt.Errorf("failed to listen on socket: %w", err)

		n.ready <- errMsg

		return errMsg
	}

	n.closeOnce = sync.OnceValue(func() error {
		n.closed.Store(true)

		return l.Close()
	})

	defer n.closeOnce()

	n.ready <- nil

	for {
		conn, err := l.Accept()
		if err != nil {
			if n.closed.Load() {
				return nil
			}

			fmt.Fprintf(os.Stderr, "failed to accept connection: %v\n", err)

			continue
		}

		go func() {
			defer func() {
				_ = conn.Close()

				if err := recover(); err != nil {
					fmt.Fprintf(os.Stderr, "recovering from panic: %v\n", err)
				}
			}()

			storage, err := n.getStorage()
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not get storage: %v\n", err)

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
				fmt.Fprintf(os.Stderr, "client disconnected with error: %v\n", err)

				return
			}
		}()
	}
}

type NbdClient struct {
	ready                 chan clientResult
	f                     *os.File
	pool                  *DevicePool
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

func (n *NbdServer) CreateClient(ctx context.Context, pool *DevicePool) *NbdClient {
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
			errs = append(errs, fmt.Errorf("failed to disconnect from server: %w", err))
		}

		err = n.f.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to close file: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (n *NbdClient) Start() error {
	var err error

	defer func() {
		if err != nil {
			n.ready <- clientResult{err: err}
		} else {
			n.ready <- clientResult{err: errors.New("closing NBD client")}
		}
	}()

	nbdPath, err := n.pool.GetDevice()
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
			select {
			case n.ready <- clientResult{path: n.path}:
			case <-n.ctx.Done():
				return
			default:
				return
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	return nil
}
