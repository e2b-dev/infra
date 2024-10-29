package nbd

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/pojntfx/go-nbd/pkg/client"
)

type Client struct {
	serverReady chan struct{}
	// This channel will only output once, either an error or nil.
	// Is is not closed.
	Ready chan error

	pool *DevicePool

	socketPath string
	DevicePath string
}

func (n *Server) NewClient(pool *DevicePool) (*Client, error) {
	path, err := pool.GetDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get nbd device: %w", err)
	}

	return &Client{
		serverReady: n.ready,
		Ready:       make(chan error),
		socketPath:  n.socketPath,
		pool:        pool,
		DevicePath:  path,
	}, nil
}

func (n *Client) ReleaseDevice(ctx context.Context) error {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		releaseErr := n.pool.ReleaseDevice(n.DevicePath)
		if releaseErr != nil {
			fmt.Fprintf(os.Stderr, "failed to release device: %s, %v\n", n.DevicePath, releaseErr)

			continue
		}

		return nil
	}
}

func (n *Client) Run(ctx context.Context) error {
	defer func() {
		go n.ReleaseDevice(context.Background())
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-n.serverReady:
	}

	d := &net.Dialer{}

	conn, err := d.DialContext(ctx, "unix", n.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	defer conn.Close()

	device, err := os.OpenFile(n.DevicePath, os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	afterDisconnect := make(chan struct{})

	defer func() {
		<-afterDisconnect

		closeErr := device.Close()
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "failed to close device: %s, %v\n", n.DevicePath, closeErr)
		}
	}()

	go func() {
		<-ctx.Done()

		disconnectErr := client.Disconnect(device)
		if disconnectErr != nil {
			fmt.Fprintf(os.Stderr, "failed to disconnect from server for device %s: %v\n", n.DevicePath, disconnectErr)
		}

		close(afterDisconnect)
	}()

	err = client.Connect(conn, device, &client.Options{
		ExportName: "default",
		// 0 means the server will choose the preferred block size
		BlockSize: uint32(0),
		OnConnected: func() {
			defer close(n.Ready)

			select {
			case n.Ready <- nil:
			case <-ctx.Done():
				n.Ready <- ctx.Err()
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	return nil
}
