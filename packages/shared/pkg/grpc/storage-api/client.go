package storageapi

import (
	"context"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is a thin wrapper over the storage-api gRPC client. A nil *Client (host
// not configured) makes every method a no-op, so callers need no nil checks.
type Client struct {
	conn   *grpc.ClientConn
	client StorageClient
}

// NewClient dials the storage-api at host over the internal mesh (insecure + otel).
// A blank host returns a nil *Client whose methods are no-ops, so the orchestrator
// can run without storage-api configured.
func NewClient(host string) (*Client, error) {
	if host == "" {
		return nil, nil
	}
	conn, err := grpc.NewClient(host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial storage-api %s: %w", host, err)
	}

	return &Client{conn: conn, client: NewStorageClient(conn)}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	return c.conn.Close()
}

// IngestBuild records a build in the storage index; a no-op on a nil/unconfigured
// client. Callers should treat errors as best-effort (the index can be backfilled).
func (c *Client) IngestBuild(ctx context.Context, req *IngestBuildRequest) error {
	if c == nil || c.client == nil {
		return nil
	}
	_, err := c.client.IngestBuild(ctx, req)

	return err
}
