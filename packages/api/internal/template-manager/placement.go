package template_manager

import (
	"context"
	loki "github.com/grafana/loki/pkg/logcli/client"
	"time"

	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type BuildPlacement interface {
	StartBuild(ctx context.Context, req *tempaltemanagergrpc.TemplateCreateRequest) error
	DeleteBuild(ctx context.Context, buildId string, templateId string) error

	GetStatus(ctx context.Context, buildId string, templateId string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error)
	GetLogs(ctx context.Context, buildId string, templateId string, offset *int32) (*[]string, error)
}

type LocalBuildPlacement struct {
	client     *GRPCClient
	lokiClient *loki.DefaultClient
}

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)
