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

		// Legacy aliases kept only for the Vector pipeline during the migration
		// window; see packages/local-dev/vector.toml and the deprecation note
		// in scripts/check-log-field-keys.sh. Do not add new raw ID keys here.
		zap.String("instanceID", sm.SandboxID), // log-key-exempt: vector-legacy
		zap.String("envID", sm.TemplateID),     // log-key-exempt: vector-legacy
	}
}
