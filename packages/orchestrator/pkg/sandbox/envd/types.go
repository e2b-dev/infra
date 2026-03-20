package envd

// SecureToken is a type alias for access tokens in the orchestrator client.
// The actual SecureToken with secure memory handling is only used in the envd service itself.
type SecureToken = string
