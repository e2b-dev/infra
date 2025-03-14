package setup

import (
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	apiTimeout = 120 * time.Second
)

var (
	APIServerURL      = utils.RequiredEnv("TESTS_API_SERVER_URL", "e.g. https://api.great-innovations.dev")
	SandboxTemplateID = utils.RequiredEnv("TESTS_SANDBOX_TEMPLATE_ID", "e.g. base")
	BuildIDToBeBuild  = utils.RequiredEnv("TESTS_SANDBOX_BUILD_ID_TO_BE_BUILD", "e.g. base")
	APIKey            = utils.RequiredEnv("TESTS_E2B_API_KEY", "your Team API key")
	AccessToken       = utils.RequiredEnv("TESTS_E2B_ACCESS_TOKEN", "your access token")

	SupabaseToken  = os.Getenv("TESTS_SUPABASE_TOKEN")
	SupabaseTeamID = os.Getenv("TESTS_SANDBOX_TEAM_ID")

	OrchestratorHost    = os.Getenv("TESTS_ORCHESTRATOR_HOST")
	TemplateManagerHost = os.Getenv("TESTS_TEMPLATE_MANAGER_HOST")
	RunBuildTest        = os.Getenv("TESTS_RUN_BUILD_TEST")
)
