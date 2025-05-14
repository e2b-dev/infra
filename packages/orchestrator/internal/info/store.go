package info

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Server struct {
	orchestrator.UnimplementedInfoServiceServer

	info      *server.ServiceInfo
	sandboxes *smap.Map[*sandbox.Sandbox]
}

func NewInfoService(_ context.Context, grpc *grpcserver.GRPCServer, info *server.ServiceInfo, sandboxes *smap.Map[*sandbox.Sandbox]) *Server {
	service := &Server{
		info:      info,
		sandboxes: sandboxes,
	}

	orchestrator.RegisterInfoServiceServer(grpc.GRPCServer(), service)

	return service
}
