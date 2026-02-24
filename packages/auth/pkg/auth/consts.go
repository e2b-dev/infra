package auth

const (
	TeamContextKey   = "team"
	UserIDContextKey = "user_id"

	// Header names for authentication.
	HeaderAPIKey        = "X-API-Key"
	HeaderAuthorization = "Authorization"
	HeaderSupabaseToken = "X-Supabase-Token"
	HeaderSupabaseTeam  = "X-Supabase-Team"
	HeaderAdminToken    = "X-Admin-Token"

	// Token prefixes.
	PrefixAPIKey      = "e2b_"
	PrefixAccessToken = "sk_e2b_"
	PrefixBearer      = "Bearer "
)
