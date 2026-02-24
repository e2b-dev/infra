package clusters

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func WithClusterFallback(clusterID *uuid.UUID) uuid.UUID {
	if clusterID == nil {
		return consts.LocalClusterID
	}

	return *clusterID
}
