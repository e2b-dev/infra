package pkg

import "os"

var OrchestratorBasePath = "/orchestrator"

func init() {
	if value := os.Getenv("ORCHESTRATOR_BASE_PATH"); value != "" {
		OrchestratorBasePath = value
	}
}
