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

func WithClusterId(clusterId uuid.UUID) zap.Field {
	return zap.String("cluster.id", clusterId.String())
}

func WithClusterNodeId(clusterNodeId string) zap.Field {
	return zap.String("cluster.node.id", clusterNodeId)
}
