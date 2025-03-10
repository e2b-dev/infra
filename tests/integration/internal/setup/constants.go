package setup

import (
	"os"
	"time"
)

const (
	apiTimeout = 120 * time.Second
)

var (
	APIServerURL      = os.Getenv("TESTS_API_SERVER_URL")
	SandboxTemplateID = os.Getenv("TESTS_SANDBOX_TEMPLATE_ID")
	APIKey            = os.Getenv("TESTS_E2B_API_KEY")

	OrchestratorHost = os.Getenv("TESTS_ORCHESTRATOR_HOST")
)
