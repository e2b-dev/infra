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
)

const (
	InstanceExpiration = time.Second * 15
	// Should we auto pause the instance by default instead of killing it,
	InstanceAutoPauseDefault = false
)

type State string

const (
	StateRunning State = "running"
	StatePaused  State = "paused"
	StateKilled  State = "killed"
	StateFailed  State = "failed"
)

var allowed = map[State]map[State]bool{
	StateRunning: {StatePaused: true, StateKilled: true, StateFailed: true},
	StatePaused:  {StateKilled: true},
}

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
		data: Data{
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
		},
		dataMu: sync.RWMutex{},
	}

	return instance
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

	State      State
	transition chan error
	Reason     error
}

type InstanceInfo struct {
	data Data

	dataMu sync.RWMutex
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
	i.dataMu.Lock()
	defer i.dataMu.Unlock()

	if !i.data.IsExpired() {
		i.data.EndTime = time.Now()
	}
}

func (i *InstanceInfo) Data() Data {
	i.dataMu.RLock()
	defer i.dataMu.RUnlock()

	return i.data
}

func (i *InstanceInfo) State() State {
	i.dataMu.RLock()
	defer i.dataMu.RUnlock()

	return i.data.State
}

// SandboxID returns the sandbox ID, safe to use without lock, it's immutable
func (i *InstanceInfo) SandboxID() string {
	return i.data.SandboxID
}

func (i *InstanceInfo) StartChangingState(ctx context.Context, newState State) (func(error), error) {
	i.dataMu.Lock()
	transition := i.data.transition
	if transition != nil {
		// If the transition is to the same state just wait
		switch {
		case i.data.State == newState:
			zap.L().Debug("State transition already in progress to the same state, waiting", logger.WithSandboxID(i.data.SandboxID), zap.String("state", string(newState)))
			i.dataMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case err := <-transition:
				if err != nil {
					zap.L().Error("State transition failed", logger.WithSandboxID(i.data.SandboxID), zap.String("state", string(newState)), zap.Error(err))
					return nil, fmt.Errorf("sandbox is in failed state")
				}
			}
			return nil, nil
		case allowed[i.data.State][newState]:
			zap.L().Debug("State transition already in progress, waiting", logger.WithSandboxID(i.data.SandboxID), zap.String("state", string(newState)))
			i.dataMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case err := <-transition:
				if err != nil {
					return nil, err
				}
			}
			zap.L().Debug("State transition already in progress, starting new transition", logger.WithSandboxID(i.data.SandboxID), zap.String("state", string(newState)))
			return i.StartChangingState(ctx, newState)
		default:
			currentState := i.data.State
			i.dataMu.Unlock()
			return nil, fmt.Errorf("invalid state transition from %s to %s", currentState, newState)
		}
	}

	defer i.dataMu.Unlock()
	if i.data.State == newState {
		zap.L().Debug("Already in the same state", logger.WithSandboxID(i.data.SandboxID), zap.String("state", string(newState)))
		return nil, nil
	}

	if _, ok := allowed[i.data.State][newState]; !ok {
		return nil, fmt.Errorf("invalid state transition from %s to %s", i.data.State, newState)
	}

	i.data.State = newState
	i.data.transition = make(chan error, 1)
	return func(err error) {
		zap.L().Debug("Transition complete", logger.WithSandboxID(i.data.SandboxID), zap.String("state", string(newState)), zap.Error(err))
		i.dataMu.Lock()
		defer i.dataMu.Unlock()

		if err != nil {
			i.data.Reason = err
			i.data.State = StateFailed
		}

		i.data.transition <- err
		close(i.data.transition)
		i.data.transition = nil
	}, nil
}

func (i *InstanceInfo) WaitForStateChange(ctx context.Context) error {
	i.dataMu.RLock()
	transition := i.data.transition
	i.dataMu.RUnlock()
	if transition == nil {
		return nil
	}

	select {
	case err := <-transition:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
