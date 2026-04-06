package backgroundworker

const (
	authCustomSchema             = "auth_custom"
	authUserProjectionQueue      = "auth_user_projection"
	authUserProjectionKind       = "auth_user_projection"
	authUserProjectionMaxWorkers = 10

	workerMeterName          = "github.com/e2b-dev/infra/packages/dashboard-api/internal/backgroundworker"
	jobResultError           = "error"
	jobResultInvalidArgument = "invalid_argument"
	jobResultSuccess         = "success"
)
