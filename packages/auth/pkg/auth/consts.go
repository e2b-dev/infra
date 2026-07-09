package auth

const (
	// Header names for authentication.
	HeaderAPIKey        = "X-API-Key"
	HeaderAuthorization = "Authorization"
	HeaderTeamID        = "X-Team-ID"
	HeaderAdminToken    = "X-Admin-Token"

	// Token prefixes.
	PrefixAPIKey      = "e2b_"
	PrefixAccessToken = "sk_e2b_"
	PrefixBearer      = "Bearer "
)
