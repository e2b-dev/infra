package template_manager

import (
	"fmt"
	"os"
	"strconv"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var host = os.Getenv("TEMPLATE_MANAGER_HOST")
var port = os.Getenv("TEMPLATE_MANAGER_PORT")

type GRPCClient struct {
	Client     template_manager.TemplateServiceClient
	connection e2bgrpc.ClientConnInterface
}

func NewClient() (*GRPCClient, error) {
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("failed to parse port: %w", err)
	}

	conn, err := e2bgrpc.GetConnection(host, portInt, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := template_manager.NewTemplateServiceClient(conn)

	return &GRPCClient{Client: client, connection: conn}, nil
}

func (a *GRPCClient) Close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}
