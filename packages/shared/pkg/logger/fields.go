package logger

import (
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func WithSandboxID(sandboxID string) zap.Field {
	return zap.String("sandbox.id", sandboxID)
}

func WithTemplateID(templateID string) zap.Field {
	return zap.String("template.id", templateID)
}

func WithBuildID(buildID string) zap.Field {
	return zap.String("build.id", buildID)
}

func WithTeamID(teamID string) zap.Field {
	return zap.String("team.id", teamID)
}

func WithNodeID(nodeID string) zap.Field {
	return zap.String("node.id", nodeID)
}

func WithClusterID(clusterID uuid.UUID) zap.Field {
	return zap.String("cluster.id", clusterID.String())
}

func WithClusterNodeID(nodeID string) zap.Field {
	return zap.String("cluster.node.id", nodeID)
}

func WithServiceInstanceID(instanceID string) zap.Field {
	return zap.String("service.instance.id", instanceID)
}
