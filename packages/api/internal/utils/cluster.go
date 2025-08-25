package utils

import "github.com/google/uuid"

func WithDefaultCluster(clusterID *uuid.UUID) uuid.UUID {
	if clusterID == nil {
		return uuid.Nil
	}

	return *clusterID
}
