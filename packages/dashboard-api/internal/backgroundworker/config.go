package backgroundworker

const (
	// must match the schema used in packages/db/pkg/auth/migrations for River tables and triggers
	AuthCustomSchema = "auth_custom"

	// must match the queue value in packages/db/pkg/auth/migrations trigger SQL inserts
	AuthUserProjectionQueue = "auth_user_projection"

	// must match the kind value in packages/db/pkg/auth/migrations trigger SQL inserts
	AuthUserProjectionKind = "auth_user_projection"

	AuthUserProjectionMaxWorkers = 10
)
