package setup

import (
	"time"
)

const (
	apiTimeout  = 120 * time.Second
	envdTimeout = 600 * time.Second
)

var (
	APIServerURL      = "https://api.e2b-jirka.dev"                    //  utils.RequiredEnv("TESTS_API_SERVER_URL", "e.g. https://api.great-innovations.dev")
	SandboxTemplateID = "base"                                         //  utils.RequiredEnv("TESTS_SANDBOX_TEMPLATE_ID", "e.g. base")
	APIKey            = "e2b_dd139a0819e9e2426e2aef47a64ef6c545b3b03a" //  utils.RequiredEnv("TESTS_E2B_API_KEY", "your Team API key")
	AccessToken       = ""                                             // utils.RequiredEnv("TESTS_E2B_ACCESS_TOKEN", "your Access token")

	SupabaseJWTSecret = "" // os.Getenv("TESTS_SUPABASE_JWT_SECRET")

	TeamID = "9ede9293-3c1d-4d88-91a0-fbc75c53adea" //  os.Getenv("TESTS_SANDBOX_TEAM_ID")
	UserID = ""                                     // os.Getenv("TESTS_SANDBOX_USER_ID")

	OrchestratorHost = "orch.xx"                   // os.Getenv("TESTS_ORCHESTRATOR_HOST")
	EnvdProxy        = "https://sbx.e2b-jirka.dev" // os.Getenv("TESTS_ENVD_PROXY")
)
