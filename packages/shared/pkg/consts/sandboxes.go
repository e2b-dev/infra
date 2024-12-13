package consts

import "os"

const NodeIDLength = 8

var OrchestratorPort = os.Getenv("ORCHESTRATOR_PORT")
