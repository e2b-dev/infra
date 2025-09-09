package store

import (
	"errors"
	"time"

	"github.com/google/uuid"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

const (
	SandboxExpiration = time.Second * 15
	// Should we auto pause the sandbox by default instead of killing it,
	SandboxAutoPauseDefault = false
)

type State string

const (
	StateRunning State = "running"
	StatePausing State = "pausing"
	StateKilling State = "killing"
	StatePaused  State = "paused"
	StateKilled  State = "killed"
)

func NewSandbox(
	SandboxID string,
	TemplateID string,
	ClientID string,
	Alias *string,
	ExecutionID string,
	TeamID uuid.UUID,
	BuildID uuid.UUID,
	Metadata map[string]string,
	MaxInstanceLength time.Duration,
	StartTime time.Time,
	EndTime time.Time,
	VCpu int64,
	TotalDiskSizeMB int64,
	RamMB int64,
	KernelVersion string,
	FirecrackerVersion string,
	EnvdVersion string,
	NodeID string,
	ClusterID uuid.UUID,
	AutoPause bool,
	EnvdAccessToken *string,
	allowInternetAccess *bool,
	BaseTemplateID string,
) *Sandbox {
	return &Sandbox{
		SandboxID:  SandboxID,
		TemplateID: TemplateID,
		ClientID:   ClientID,
		Alias:      Alias,

		ExecutionID:         ExecutionID,
		TeamID:              TeamID,
		BuildID:             BuildID,
		Metadata:            Metadata,
		MaxInstanceLength:   MaxInstanceLength,
		StartTime:           StartTime,
		EndTime:             EndTime,
		VCpu:                VCpu,
		TotalDiskSizeMB:     TotalDiskSizeMB,
		RamMB:               RamMB,
		KernelVersion:       KernelVersion,
		FirecrackerVersion:  FirecrackerVersion,
		EnvdVersion:         EnvdVersion,
		EnvdAccessToken:     EnvdAccessToken,
		AllowInternetAccess: allowInternetAccess,
		NodeID:              NodeID,
		ClusterID:           ClusterID,
		AutoPause:           AutoPause,
		BaseTemplateID:      BaseTemplateID,
		State:               StateRunning,
	}
}

type Sandbox struct {
	SandboxID  string  `json:"sandbox_id"`
	TemplateID string  `json:"template_id"`
	ClientID   string  `json:"client_id"`
	Alias      *string `json:"alias"`

	ExecutionID         string            `json:"execution_id"`
	TeamID              uuid.UUID         `json:"team_id"`
	BuildID             uuid.UUID         `json:"build_id"`
	BaseTemplateID      string            `json:"base_template_id"`
	Metadata            map[string]string `json:"metadata"`
	MaxInstanceLength   time.Duration     `json:"max_instance_length"`
	StartTime           time.Time         `json:"start_time"`
	EndTime             time.Time         `json:"end_time"`
	VCpu                int64             `json:"v_cpu"`
	TotalDiskSizeMB     int64             `json:"total_disk_size_mb"`
	RamMB               int64             `json:"ram_mb"`
	KernelVersion       string            `json:"kernel_version"`
	FirecrackerVersion  string            `json:"firecracker_version"`
	EnvdVersion         string            `json:"envd_version"`
	EnvdAccessToken     *string           `json:"envd_access_token"`
	AllowInternetAccess *bool             `json:"allow_internet_access"`
	NodeID              string            `json:"node_id"`
	ClusterID           uuid.UUID         `json:"cluster_id"`
	AutoPause           bool              `json:"auto_pause"`
	State               State             `json:"state"`
}

func (i *Sandbox) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  i.SandboxID,
		TemplateID: i.TemplateID,
		TeamID:     i.TeamID.String(),
	}
}

var (
	ErrAlreadyBeingPaused  = errors.New("sandbox is already being paused")
	ErrAlreadyBeingDeleted = errors.New("sandbox is already being removed")
)
