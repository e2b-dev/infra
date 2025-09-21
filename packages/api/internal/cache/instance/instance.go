package instance

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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
) Data {
	return Data{
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

type Data struct {
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

type InstanceInfo struct {
	_data Data

	transition *utils.ErrorOnce
	mu         sync.RWMutex
}

func NewInstanceInfo(data Data) *InstanceInfo {
	return &InstanceInfo{
		_data: data,
	}
}

func (i Data) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  i.SandboxID,
		TemplateID: i.TemplateID,
		TeamID:     i.TeamID.String(),
	}
}

func (i Data) IsExpired() bool {
	return time.Now().After(i.EndTime)
}

func (i *InstanceInfo) SetExpired() {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.setExpired()
}

func (i *InstanceInfo) setExpired() {
	if !i._data.IsExpired() {
		i._data.EndTime = time.Now()
	}
}

func (i *InstanceInfo) Data() Data {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i._data
}

func (i *InstanceInfo) State() State {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i._data.State
}

// SandboxID returns the sandbox ID, safe to use without lock, it's immutable
func (i *InstanceInfo) SandboxID() string {
	return i._data.SandboxID
}

// TeamID returns the team ID, safe to use without lock, it's immutable
func (i *InstanceInfo) TeamID() uuid.UUID {
	return i._data.TeamID
}

func (i *InstanceInfo) startRemoving(ctx context.Context, stateAction StateAction) (alreadyDone bool, callback func(error), err error) {
	newState := StateKilling
	if stateAction == StateActionPause {
		newState = StatePausing
	}

	i.mu.Lock()
	transition := i.transition
	if transition != nil {
		currentState := i._data.State
		i.mu.Unlock()

		if currentState != newState && !allowed[currentState][newState] {
			return false, nil, fmt.Errorf("invalid state transition, already in transition from %s", currentState)
		}

		zap.L().Debug("State transition already in progress to the same state, waiting", logger.WithSandboxID(i.SandboxID()), zap.String("state", string(newState)))
		err = transition.WaitWithContext(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("sandbox is in failed state: %w", err)
		}

		// If the transition is to the same state just wait
		switch {
		case currentState == newState:
			return true, func(err error) {}, nil
		case allowed[currentState][newState]:
			return i.startRemoving(ctx, stateAction)
		default:
			return false, nil, fmt.Errorf("unexpected state transition")
		}
	}

	defer i.mu.Unlock()
	if i._data.State == newState {
		zap.L().Debug("Already in the same state", logger.WithSandboxID(i.SandboxID()), zap.String("state", string(newState)))
		return true, func(error) {}, nil
	}

	if _, ok := allowed[i._data.State][newState]; !ok {
		return false, nil, fmt.Errorf("invalid state transition from %s to %s", i._data.State, newState)
	}

	i.setExpired()
	i._data.State = newState
	i.transition = utils.NewErrorOnce()

	callback = func(err error) {
		zap.L().Debug("Transition complete", logger.WithSandboxID(i.SandboxID()), zap.String("state", string(newState)), zap.Error(err))
		i.mu.Lock()
		defer i.mu.Unlock()

		setErr := i.transition.SetError(err)
		if err != nil {
			// Keep the transition in place so the error stays
			zap.L().Error("Failed to set transition result", logger.WithSandboxID(i.SandboxID()), zap.Error(setErr))
			return
		}

		// The transition is completed and the next transition can be started
		i.transition = nil
	}

	return false, callback, nil
}

func (i *InstanceInfo) WaitForStateChange(ctx context.Context) error {
	i.mu.RLock()
	transition := i.transition
	i.mu.RUnlock()
	if transition == nil {
		return nil
	}

	return transition.WaitWithContext(ctx)
}

func (i *InstanceInfo) ExtendEndTime(newEndTime time.Time, allowShorter bool) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	// If shorter than the current end time, don't extend
	if !allowShorter && newEndTime.Before(i._data.EndTime) {
		return false
	}

	i._data.EndTime = newEndTime

	return true
}
