package auth

const (
	// Header names for authentication.
	HeaderAPIKey        = "X-API-Key"
	HeaderAuthorization = "Authorization"
	HeaderSupabaseToken = "X-Supabase-Token"
	HeaderSupabaseTeam  = "X-Supabase-Team"
	HeaderTeamID        = "X-Team-Id"
	HeaderAdminToken    = "X-Admin-Token"

	// Token prefixes.
	PrefixAPIKey      = "e2b_"
	PrefixAccessToken = "sk_e2b_"
	PrefixBearer      = "Bearer "
)
