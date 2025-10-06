package pkg

import "os"

func OrchestratorBasePath() string {
	if value := os.Getenv("ORCHESTRATOR_BASE_PATH"); value != "" {
		return value
	}

	return "/orchestrator"
}
