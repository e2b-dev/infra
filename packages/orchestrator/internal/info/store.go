package info

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/servicetype"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

var (
	serviceRolesMapper = map[servicetype.ServiceType]orchestratorinfo.ServiceInfoRole{
		servicetype.Orchestrator:    orchestratorinfo.ServiceInfoRole_Orchestrator,
		servicetype.TemplateManager: orchestratorinfo.ServiceInfoRole_TemplateManager,
	}
)

type Server struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	info      *server.ServiceInfo
	sandboxes *smap.Map[*sandbox.Sandbox]
}

func NewInfoService(_ context.Context, grpc *grpcserver.GRPCServer, info *server.ServiceInfo, sandboxes *smap.Map[*sandbox.Sandbox]) *Server {
	service := &Server{
		info:      info,
		sandboxes: sandboxes,
	}

	orchestratorinfo.RegisterInfoServiceServer(grpc.GRPCServer(), service)

	return service
}

func NewInfoContainer(clientId string, sourceVersion string, sourceCommit string) *server.ServiceInfo {
	services := servicetype.GetServices()
	serviceRoles := make([]orchestratorinfo.ServiceInfoRole, 0)

	for _, service := range services {
		if role, ok := serviceRolesMapper[service]; ok {
			serviceRoles = append(serviceRoles, role)
		}
	}

	serviceInfo := &server.ServiceInfo{
		ClientId:  clientId,
		ServiceId: uuid.NewString(),
		Startup:   time.Now(),
		Roles:     serviceRoles,

		SourceVersion: sourceVersion,
		SourceCommit:  sourceCommit,
	}

	serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy)

	return serviceInfo
}
