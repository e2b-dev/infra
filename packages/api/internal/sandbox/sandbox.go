package sandbox

import (
	"time"

	"github.com/google/uuid"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func NewSandbox(
	sandboxID string,
	templateID string,
	clientID string,
	alias *string,
	executionID string,
	teamID uuid.UUID,
	buildID uuid.UUID,
	metadata map[string]string,
	maxInstanceLength time.Duration,
	startTime time.Time,
	endTime time.Time,
	vcpu int64,
	totalDiskSizeMB int64,
	ramMB int64,
	kernelVersion string,
	firecrackerVersion string,
	envdVersion string,
	nodeID string,
	clusterID uuid.UUID,
	autoPause bool,
	envdAccessToken *string,
	allowInternetAccess *bool,
	baseTemplateID string,
) Sandbox {
	return Sandbox{
		SandboxID:  sandboxID,
		TemplateID: templateID,
		ClientID:   clientID,
		Alias:      alias,

		ExecutionID:         executionID,
		TeamID:              teamID,
		BuildID:             buildID,
		Metadata:            metadata,
		MaxInstanceLength:   maxInstanceLength,
		StartTime:           startTime,
		EndTime:             endTime,
		VCpu:                vcpu,
		TotalDiskSizeMB:     totalDiskSizeMB,
		RamMB:               ramMB,
		KernelVersion:       kernelVersion,
		FirecrackerVersion:  firecrackerVersion,
		EnvdVersion:         envdVersion,
		EnvdAccessToken:     envdAccessToken,
		AllowInternetAccess: allowInternetAccess,
		NodeID:              nodeID,
		ClusterID:           clusterID,
		AutoPause:           autoPause,
		State:               StateRunning,
		BaseTemplateID:      baseTemplateID,
	}
}

type Sandbox struct {
	SandboxID  string
	TemplateID string
	ClientID   string
	Alias      *string

	ExecutionID         string
	TeamID              uuid.UUID
	BuildID             uuid.UUID
	BaseTemplateID      string
	Metadata            map[string]string
	MaxInstanceLength   time.Duration
	StartTime           time.Time
	EndTime             time.Time
	VCpu                int64
	TotalDiskSizeMB     int64
	RamMB               int64
	KernelVersion       string
	FirecrackerVersion  string
	EnvdVersion         string
	EnvdAccessToken     *string
	AllowInternetAccess *bool
	NodeID              string
	ClusterID           uuid.UUID
	AutoPause           bool

	State State
}

func (s Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  s.SandboxID,
		TemplateID: s.TemplateID,
		TeamID:     s.TeamID.String(),
	}
}

func (s Sandbox) IsExpired() bool {
	return time.Now().After(s.EndTime)
}
