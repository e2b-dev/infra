package database

const (
	SandboxStatusPending    = "pending"
	SandboxStatusRunning    = "running"
	SandboxStatusPaused     = "paused"
	SandboxStatusTerminated = "terminated"

	OrchestratorStatusInitializing = "initializing"
	OrchestratorStatusRunning      = "running"
	OrchestratorStatusDraining     = "draining"
	OrchestratorStatusTerminated   = "terminated"
	OrchestratorStatusQuarantined  = "quarantined"
)
