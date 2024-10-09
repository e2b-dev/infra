package nbd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
)

const (
	nbdDeviceAcquireTimeout = 10 * time.Second
	nbdDeviceAcquireDelay   = 10 * time.Millisecond
)

type Nbd struct {
	pool       *NbdDevicePool
	Path       string
	SocketPath string
	backend    block.Device
}

func (n *Nbd) Close() error {
	releaseErr := n.pool.ReleaseDevice(n.Path)

	return errors.Join(releaseErr)
}

func NewNbd(ctx context.Context, s block.Device, pool *NbdDevicePool, socketPath string) (*Nbd, error) {
	nbdDev, err := pool.GetDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get nbd device: %w", err)
	}

	defer func() {
		if err != nil {
			releaseErr := pool.ReleaseDevice(nbdDev)
			if releaseErr != nil {
				fmt.Fprintf(os.Stderr, "failed to release device: %s, %v", nbdDev, releaseErr)
			}
		}
	}()

	return &Nbd{
		pool:       pool,
		Path:       nbdDev,
		backend:    s,
		SocketPath: socketPath,
	}, nil
}

func (n *Nbd) StartServer() error {
	l, err := net.Listen("unix", n.SocketPath)
	if err != nil {
		return err
	}
	defer l.Close()

	log.Println("Listening on", l.Addr())

	clients := 0
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Println("Could not accept connection, continuing:", err)

			continue
		}

		clients++

		log.Printf("%v clients connected", clients)

		go func() {
			defer func() {
				_ = conn.Close()

				clients--

				if err := recover(); err != nil {
					log.Printf("Client disconnected with error: %v", err)
				}

				log.Printf("%v clients connected", clients)
			}()

			if err := server.Handle(
				conn,
				[]*server.Export{
					{
						Name:        "default",
						Description: "The default export",
						Backend:     n.backend,
					},
				},
				&server.Options{
					ReadOnly:           false,
					MinimumBlockSize:   uint32(1),
					PreferredBlockSize: uint32(4096),
					MaximumBlockSize:   uint32(0xffffffff),
					SupportsMultiConn:  true,
				}); err != nil {
				panic(err)
			}
		}()
	}
}

func (n *Nbd) StartClient() error {
	conn, err := net.Dial("unix", n.SocketPath)
	if err != nil {
		log.Println("Could not connect to server:", err)
		return err
	}
	defer conn.Close()

	log.Println("Connected to", conn.RemoteAddr())

	f, err := os.OpenFile(n.Path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := client.Connect(conn, f, &client.Options{
		ExportName: "default",
		BlockSize:  uint32(4096),
	}); err != nil {
		return err
	}

	return nil
}
