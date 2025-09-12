package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	InstanceExpiration = time.Second * 15
	// Should we auto pause the instance by default instead of killing it,
	InstanceAutoPauseDefault = false
)

type State string

const (
	StateRunning State = "running"
	StatePausing State = "pausing"
	StateKilling State = "killing"
	StatePaused  State = "paused"
	StateKilled  State = "killed"
)

func NewInstanceInfo(
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
) *InstanceInfo {
	instance := &InstanceInfo{
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
		endTime:             endTime,
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
		state:               StateRunning,
		stopping:            utils.NewSetOnce[struct{}](),
		BaseTemplateID:      baseTemplateID,
		mu:                  sync.RWMutex{},
	}

	return instance
}

type InstanceInfo struct {
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
	endTime             time.Time
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

	state State
	mu    sync.RWMutex

	stopping *utils.SetOnce[struct{}]
}

func (i *InstanceInfo) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  i.SandboxID,
		TemplateID: i.TemplateID,
		TeamID:     i.TeamID.String(),
	}
}

func (i *InstanceInfo) IsExpired() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i.isExpired()
}

func (i *InstanceInfo) isExpired() bool {
	return time.Now().After(i.endTime)
}

func (i *InstanceInfo) GetEndTime() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i.endTime
}

func (i *InstanceInfo) SetEndTime(endTime time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.setEndTime(endTime)
}

func (i *InstanceInfo) setEndTime(endTime time.Time) {
	i.endTime = endTime
}

func (i *InstanceInfo) SetExpired() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.setExpired()
}

func (i *InstanceInfo) setExpired() {
	if !i.isExpired() {
		i.setEndTime(time.Now())
	}
}

func (i *InstanceInfo) GetState() State {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i.state
}

var (
	ErrAlreadyBeingPaused  = errors.New("instance is already being paused")
	ErrAlreadyBeingDeleted = errors.New("instance is already being removed")
)

func (i *InstanceInfo) markRemoving(removeType RemoveType) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.state != StateRunning {
		if i.state == StatePausing || i.state == StatePaused {
			return ErrAlreadyBeingPaused
		} else {
			return ErrAlreadyBeingDeleted
		}
	}
	// Set remove type
	if removeType == RemoveTypePause {
		i.state = StatePausing
	} else {
		i.state = StateKilling
	}

	// Mark the stop time
	i.setExpired()

	return nil
}

func (i *InstanceInfo) WaitForStop(ctx context.Context) error {
	if i.GetState() == StateRunning {
		return fmt.Errorf("sandbox isn't stopping")
	}

	_, err := i.stopping.WaitWithContext(ctx)
	return err
}

func (i *InstanceInfo) stopDone(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.state == StatePausing {
		i.state = StatePaused
	} else {
		i.state = StateKilled
	}

	if err != nil {
		err := i.stopping.SetError(err)
		if err != nil {
			zap.L().Error("error setting stopDone value", zap.Error(err))
		}
	} else {
		err := i.stopping.SetValue(struct{}{})
		if err != nil {
			zap.L().Error("error setting stopDone value", zap.Error(err))
		}
	}
}
