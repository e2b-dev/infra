package consts

import "os"

const NodeIDLength = 8

var (
	OrchestratorPort = os.Getenv("ORCHESTRATOR_PORT")
	SessionProxyPort = os.Getenv("SESSION_PROXY_PORT")
)
