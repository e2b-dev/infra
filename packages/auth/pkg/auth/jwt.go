package auth

const (
	// MinJWTSecretLength is the minimum length of a secret used to verify the Supabase JWT.
	// This is a security measure to prevent the use of weak secrets (like empty).
	MinJWTSecretLength = 16
)
