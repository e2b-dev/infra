package proxygrpc

const (
	// MetadataTrafficAccessToken is forwarded by client-proxy when present.
	MetadataTrafficAccessToken = "e2b-traffic-access-token"
	// MetadataSandboxRequestPort identifies the original requested sandbox port.
	MetadataSandboxRequestPort = "e2b-sandbox-request-port"
)
