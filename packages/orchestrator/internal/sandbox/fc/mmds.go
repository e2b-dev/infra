package fc

import (
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxId            string `json:"instanceID"`
	TemplateId           string `json:"envID"`
	LogsCollectorAddress string `json:"address"`
	TraceId              string `json:"traceID"`
	TeamId               string `json:"teamID"`
}

func (mm MmdsMetadata) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  mm.SandboxId,
		TemplateID: mm.TemplateId,
		TeamID:     mm.TeamId,
	}
}
