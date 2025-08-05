package node

type NodeInfo struct {
	// Corresponds to Nomad short ID that is different from NodeID provided by the orchestrator itself.
	ID string

	OrchestratorAddress string
	IPAddress           string
}
