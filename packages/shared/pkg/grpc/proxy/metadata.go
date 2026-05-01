package proxygrpc

const (
	// MetadataTrafficAccessToken is forwarded by client-proxy when present.
	MetadataTrafficAccessToken = "e2b-traffic-access-token"
	// MetadataSandboxRequestPort identifies the original requested sandbox port.
	MetadataSandboxRequestPort = "e2b-sandbox-request-port"
	// MetadataEnvdAccessToken is forwarded by client-proxy for envd traffic on secure sandboxes.
	MetadataEnvdAccessToken = "e2b-envd-access-token"
	// MetadataAuthorization carries bearer credentials for gRPC requests.
	MetadataAuthorization = "authorization"
	// ScopeSandboxLifecycle authorizes client-proxy sandbox lifecycle operations.
	ScopeSandboxLifecycle = "sandboxes:lifecycle"
	// MetadataEnvdHTTPAccessToken is the HTTP header used by envd for access token authentication.
	MetadataEnvdHTTPAccessToken = "X-Access-Token"
)
