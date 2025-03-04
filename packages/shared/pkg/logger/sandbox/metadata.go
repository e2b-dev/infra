package sbxlogger

import "go.uber.org/zap"

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
		zap.String("sandboxID", sm.SandboxID),
		zap.String("templateID", sm.TemplateID),
		zap.String("teamID", sm.TeamID),

		// Fields for Vector
		zap.String("instanceID", sm.SandboxID),
		zap.String("envID", sm.TemplateID),
	}
}
