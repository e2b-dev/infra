package utils

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func WithDefaultCluster(clusterID *uuid.UUID) uuid.UUID {
	if clusterID == nil {
		return consts.DefaultClusterID
	}

	return *clusterID
}
