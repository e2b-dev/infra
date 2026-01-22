package logger

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	DebugID = "debug_id"
)

func GetDebugID(ctx context.Context) *string {
	if ctx.Value(DebugID) == nil {
		return nil
	}

	value := ctx.Value(DebugID).(string)

	return &value
}

func WithSandboxID(sandboxID string) zap.Field {
	return zap.String("sandbox.id", sandboxID)
}

func WithTemplateID(templateID string) zap.Field {
	return zap.String("template.id", templateID)
}

func WithBuildID(buildID string) zap.Field {
	return zap.String("build.id", buildID)
}

func WithExecutionID(executionID string) zap.Field {
	return zap.String("execution.id", executionID)
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

func WithServiceInstanceID(instanceID string) zap.Field {
	return zap.String("service.instance.id", instanceID)
}

func WithEnvdVersion(envdVersion string) zap.Field {
	return zap.String("envd.version", envdVersion)
}
