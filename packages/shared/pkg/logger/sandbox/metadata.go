package sbxlogger

import (
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxMetadata struct {
	SandboxID  string
	TemplateID string
	TeamID     string
}

type LoggerMetadata interface {
	LoggerMetadata() SandboxMetadata
}

func (sm SandboxMetadata) LoggerMetadata() SandboxMetadata {
	return sm
}

func (sm SandboxMetadata) Fields() []zap.Field {
	return []zap.Field{
		logger.WithSandboxID(sm.SandboxID),
		logger.WithTemplateID(sm.TemplateID),
		logger.WithTeamID(sm.TeamID),

		// Fields for Vector
		zap.String("instanceID", sm.SandboxID),
		zap.String("envID", sm.TemplateID),
	}
}
