package sandbox

import (
	"time"
)

// TransitionEffect describes what happens to the sandbox when a state action completes.
type TransitionEffect int

const (
	// TransitionExpires marks the sandbox as expired (terminal removal).
	TransitionExpires TransitionEffect = iota
	// TransitionTransient is temporary — the sandbox is restored to its
	// previous state once the transition completes successfully.
	TransitionTransient
)

type StateAction struct {
	// Name is the human-readable identifier for this action (e.g. "pause", "kill").
	Name string
	// TargetState is the sandbox state this action transitions to.
	TargetState State
	// Effect describes whether the transition is terminal or transient.
	Effect TransitionEffect
}

var (
	StateActionPause = StateAction{
		Name:        "pause",
		TargetState: StatePausing,
		Effect:      TransitionExpires,
	}
	StateActionKill = StateAction{
		Name:        "kill",
		TargetState: StateKilling,
		Effect:      TransitionExpires,
	}
	StateActionSnapshot = StateAction{
		Name:        "snapshot",
		TargetState: StateSnapshotting,
		Effect:      TransitionTransient,
	}
)

var AllowedTransitions = map[State]map[State]bool{
	StateRunning:      {StatePausing: true, StateKilling: true, StateSnapshotting: true},
	StatePausing:      {StateKilling: true},
	StateSnapshotting: {StateRunning: true, StateKilling: true, StatePausing: true},
}

const (
	SandboxTimeoutDefault = time.Second * 15
	// Should we auto pause the instance by default instead of killing it
	AutoPauseDefault = false
)

type State string

const (
	StateRunning      State = "running"
	StatePausing      State = "pausing"
	StateKilling      State = "killing"
	StateSnapshotting State = "snapshotting"
)
