package custom

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/local_storage/nbd"
	"github.com/pojntfx/go-nbd/pkg/client"
)

type Client struct {
	serverReady chan struct{}
	// This channel will only output once, either an error or nil.
	// Is is not closed.
	Ready chan error

	device     *os.File
	socketPath string
	DevicePath string
	id         string
}

func (n *Server) NewClient(ctx context.Context, id string) (*Client, error) {
	path, err := nbd.Pool.GetDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get nbd device: %w", err)
	}

	log.Printf("[%s] got device %s", id, path)

	device, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &Client{
		device:      device,
		serverReady: n.ready,
		Ready:       make(chan error),
		socketPath:  n.socketPath,
		DevicePath:  path,
		id:          id,
	}, nil
}

func (n *Client) Close() error {
	err := client.Disconnect(n.device)
	if err != nil {
		log.Printf("[%s] failed to disconnect: %v\n", n.id, err)
	}

	for {
		releaseErr := nbd.Pool.ReleaseDevice(n.DevicePath)
		if releaseErr != nil {

			time.Sleep(100 * time.Millisecond)

			continue
		}

		log.Printf("[%s] released device %s", n.id, n.DevicePath)

		return nil
	}
}

func (n *Client) Run() error {
	_, ok := <-n.serverReady
	if !ok {
		return fmt.Errorf("server ready channel closed")
	}

	d := &net.Dialer{}

	conn, err := d.DialContext(context.Background(), "unix", n.socketPath)
	if err != nil {
		return fmt.Errorf("failed to dial server: %w", err)
	}

	err = client.Connect(conn, n.device, &client.Options{
		ExportName: "default",
		// 0 means the server will choose the preferred block size
		BlockSize: 4096,
		OnConnected: func() {
			n.Ready <- nil
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	return nil
}
