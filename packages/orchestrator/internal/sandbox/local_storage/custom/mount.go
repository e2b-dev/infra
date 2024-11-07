package custom

import (
	"context"
	"fmt"
	"log"

	"github.com/pojntfx/go-nbd/pkg/backend"

	"golang.org/x/sync/errgroup"
)

type RootfsOverlay struct {
	storage backend.Backend
	server  *Server
	client  *Client
}

func NewMount(ctx context.Context, socketPath string, storage backend.Backend, id string) (*RootfsOverlay, error) {
	server := NewServer(socketPath, storage, id)

	client, err := server.NewClient(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("error creating nbd client: %w", err)
	}

	return &RootfsOverlay{
		storage: storage,
		server:  server,
		client:  client,
	}, nil
}

func (o *RootfsOverlay) Run() error {
	var eg errgroup.Group

	eg.Go(func() error {
		log.Printf("[%s] starting nbd server", o.client.id)

		err := o.server.Run()
		if err != nil {
			return fmt.Errorf("error running nbd server: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		log.Printf("[%s] starting nbd client", o.client.id)
		err := o.client.Run()
		if err != nil {
			return fmt.Errorf("error running nbd client: %w", err)
		}

		return nil
	})

	err := eg.Wait()
	if err != nil {
		return fmt.Errorf("error waiting for nbd server and client: %w", err)
	}

	return nil
}

func (o *RootfsOverlay) Close() error {
	return o.client.Close()
}

// Path can only be called once.
func (o *RootfsOverlay) Path() (string, error) {
	err, ok := <-o.client.Ready
	if !ok {
		return "", fmt.Errorf("nbd client closed or getting path called multiple times")
	}

	if err != nil {
		return "", fmt.Errorf("error getting nbd path: %w", err)
	}

	return o.client.DevicePath, nil
}
