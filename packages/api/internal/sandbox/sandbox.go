package sandbox

import (
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
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
	autoResume *types.SandboxAutoResumeConfig,
	envdAccessToken *string,
	allowInternetAccess *bool,
	baseTemplateID string,
	domain *string,
	network *types.SandboxNetworkConfig,
	trafficAccessToken *string,
) Sandbox {
	return Sandbox{
		SandboxID:  sandboxID,
		TemplateID: templateID,
		ClientID:   clientID,
		Alias:      alias,
		Domain:     domain,

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
		TrafficAccessToken:  trafficAccessToken,
		AllowInternetAccess: allowInternetAccess,
		NodeID:              nodeID,
		ClusterID:           clusterID,
		AutoPause:           autoPause,
		AutoResume:          autoResume,
		State:               StateRunning,
		BaseTemplateID:      baseTemplateID,
		Network:             network,
	}
}

type Sandbox struct {
	SandboxID  string  `json:"sandboxID"`
	TemplateID string  `json:"templateID"`
	ClientID   string  `json:"clientID"`
	Alias      *string `json:"alias,omitempty"`
	Domain     *string `json:"domain,omitempty"`

	ExecutionID         string                         `json:"executionID"`
	TeamID              uuid.UUID                      `json:"teamID"`
	BuildID             uuid.UUID                      `json:"buildID"`
	BaseTemplateID      string                         `json:"baseTemplateID"`
	Metadata            map[string]string              `json:"metadata"`
	MaxInstanceLength   time.Duration                  `json:"maxInstanceLength"`
	StartTime           time.Time                      `json:"startTime"`
	EndTime             time.Time                      `json:"endTime"`
	VCpu                int64                          `json:"vCpu"`
	TotalDiskSizeMB     int64                          `json:"totalDiskSizeMB"`
	RamMB               int64                          `json:"ramMB"`
	KernelVersion       string                         `json:"kernelVersion"`
	FirecrackerVersion  string                         `json:"firecrackerVersion"`
	EnvdVersion         string                         `json:"envdVersion"`
	EnvdAccessToken     *string                        `json:"envdAccessToken,omitempty"`
	TrafficAccessToken  *string                        `json:"trafficAccessToken"`
	AllowInternetAccess *bool                          `json:"allowInternetAccess,omitempty"`
	NodeID              string                         `json:"nodeID"`
	ClusterID           uuid.UUID                      `json:"clusterID"`
	AutoPause           bool                           `json:"autoPause"`
	AutoResume          *types.SandboxAutoResumeConfig `json:"autoResume,omitempty"`
	Network             *types.SandboxNetworkConfig    `json:"network"`

	State State `json:"state"`
}

func (s Sandbox) ToAPISandbox() *api.Sandbox {
	return &api.Sandbox{
		SandboxID:          s.SandboxID,
		TemplateID:         s.TemplateID,
		ClientID:           s.ClientID,
		Alias:              s.Alias,
		EnvdVersion:        s.EnvdVersion,
		EnvdAccessToken:    s.EnvdAccessToken,
		TrafficAccessToken: s.TrafficAccessToken,
		Domain:             s.Domain,
	}
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
