package template_manager

import (
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
)

type BuildClient struct {
	GRPC *clusters.ClusterGRPC
}
