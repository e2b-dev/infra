package template_manager

import (
	"github.com/e2b-dev/infra/packages/api/internal/edge"
)

type BuildClient struct {
	GRPC *edge.ClusterGRPC
}
