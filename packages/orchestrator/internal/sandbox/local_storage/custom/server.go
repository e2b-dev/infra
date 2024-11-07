package custom

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/server"
)

const blockSize = 4096

type Server struct {
	storage    backend.Backend
	ready      chan struct{}
	socketPath string
	id         string
}

func NewServer(socketPath string, storage backend.Backend, id string) *Server {
	return &Server{
		storage:    storage,
		socketPath: socketPath,
		ready:      make(chan struct{}),
		id:         id,
	}
}

func (n *Server) Run() error {
	defer close(n.ready)

	var lc net.ListenConfig

	l, err := lc.Listen(context.Background(), "unix", n.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	// defer l.Close()

	n.ready <- struct{}{}

	conn, acceptErr := l.Accept()
	if acceptErr != nil {
		return fmt.Errorf("failed to accept connection: %w", acceptErr)
	}
	// defer conn.Close()

	err = server.Handle(
		conn,
		[]*server.Export{
			{
				Backend: n.storage,
				Name:    "default",
			},
		},
		&server.Options{
			ReadOnly:           false,
			MinimumBlockSize:   blockSize,
			PreferredBlockSize: blockSize,
			MaximumBlockSize:   blockSize,
			SupportsMultiConn:  false,
		})
	if err != nil {
		return fmt.Errorf("failed to handle client: %w", err)
	}

	log.Printf("[%s] client disconnected without error\n", n.id)

	return nil
}
