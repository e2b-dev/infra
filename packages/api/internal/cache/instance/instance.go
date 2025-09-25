package instance

import (
	"sync"
	"time"

	"github.com/google/uuid"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	SandboxTimeoutDefault = time.Second * 15
	// Should we auto pause the instance by default instead of killing it,
	AutoPauseDefault = false
)

type State string

const (
	StateRunning State = "running"
	StatePausing State = "pausing"
	StateKilling State = "killing"
)

var allowed = map[State]map[State]bool{
	StateRunning: {StatePausing: true, StateKilling: true},
	StatePausing: {StateKilling: true},
}

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

type memorySandbox struct {
	_data Sandbox

	transition *utils.ErrorOnce
	mu         sync.RWMutex
}

func newMemorySandbox(data Sandbox) *memorySandbox {
	return &memorySandbox{
		_data: data,
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

func (i *memorySandbox) SetExpired() {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.setExpired()
}

func (i *memorySandbox) setExpired() {
	if !i._data.IsExpired() {
		i._data.EndTime = time.Now()
	}
}

func (i *memorySandbox) Data() Sandbox {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i._data
}

func (i *memorySandbox) State() State {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i._data.State
}

// SandboxID returns the sandbox ID, safe to use without lock, it's immutable
func (i *memorySandbox) SandboxID() string {
	return i._data.SandboxID
}

// TeamID returns the team ID, safe to use without lock, it's immutable
func (i *memorySandbox) TeamID() uuid.UUID {
	return i._data.TeamID
}

func (i *memorySandbox) extendEndTime(newEndTime time.Time, allowShorter bool) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	// If shorter than the current end time, don't extend
	if !allowShorter && newEndTime.Before(i._data.EndTime) {
		return false
	}

	i._data.EndTime = newEndTime

	return true
}
