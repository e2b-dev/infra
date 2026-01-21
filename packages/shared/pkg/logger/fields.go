package logger

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	DebugID = "debug_id"

	SandboxIDContextKey  = "sandbox.id"
	TeamIDIDContextKey   = "team.id"
	BuildIDContextKey    = "build.id"
	TemplateIDContextKey = "template.id"
)

func GetDebugID(ctx context.Context) *string {
	if ctx.Value(DebugID) == nil {
		return nil
	}

	value := ctx.Value(DebugID).(string)

	return &value
}

// GetSandboxID retrieves the sandbox ID from context if present.
func GetSandboxID(ctx context.Context) *string {
	if ctx.Value(SandboxIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(SandboxIDContextKey).(string)

	return &value
}

func GetTeamID(ctx context.Context) *string {
	if ctx.Value(TeamIDIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(TeamIDIDContextKey).(string)

	return &value
}

func GetBuildID(ctx context.Context) *string {
	if ctx.Value(BuildIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(BuildIDContextKey).(string)

	return &value
}

func GetTemplateID(ctx context.Context) *string {
	if ctx.Value(TemplateIDContextKey) == nil {
		return nil
	}

	value := ctx.Value(TemplateIDContextKey).(string)

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

func FieldsFromContext(ctx context.Context) []zap.Field {
	var attrs []zap.Field

	if sandboxID := GetSandboxID(ctx); sandboxID != nil {
		attrs = append(attrs, WithSandboxID(*sandboxID))
	}

	if teamID := GetTeamID(ctx); teamID != nil {
		attrs = append(attrs, WithTeamID(*teamID))
	}

	if buildID := GetBuildID(ctx); buildID != nil {
		attrs = append(attrs, WithBuildID(*buildID))
	}

	if templateID := GetTemplateID(ctx); templateID != nil {
		attrs = append(attrs, WithTemplateID(*templateID))
	}

	return attrs
}
