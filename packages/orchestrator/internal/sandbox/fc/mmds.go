package fc

import (
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxID  string `json:"instanceID"`
	TemplateID string `json:"envID"`
	TraceID    string `json:"traceID"`
	TeamID     string `json:"teamID"`

	LogsCollectorAddress string `json:"address"`
}

func (mm MmdsMetadata) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  mm.SandboxID,
		TemplateID: mm.TemplateID,
		TeamID:     mm.TeamID,
	}
}
