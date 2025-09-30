package sandbox

import (
	"time"
)

type StateAction string

var AllowedTransitions = map[State]map[State]bool{
	StateRunning: {StatePausing: true, StateKilling: true},
	StatePausing: {StateKilling: true},
}

const (
	StateActionPause StateAction = "pause"
	StateActionKill  StateAction = "kill"
)

const (
	SandboxTimeoutDefault = time.Second * 15
	// Should we auto pause the instance by default instead of killing it
	AutoPauseDefault = false
)

type State string

const (
	StateRunning State = "running"
	StatePausing State = "pausing"
	StateKilling State = "killing"
)
