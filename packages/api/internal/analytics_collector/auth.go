package analyticscollector

import (
	"context"
)

type gRPCApiKey struct {
	apiKey string
	// requireTransportSecurity mirrors the transport credentials the
	// connection was created with. gRPC refuses to attach per-RPC credentials
	// that require transport security to a plaintext connection, so a
	// plaintext collector (local development) must relax this.
	requireTransportSecurity bool
}

func newGRPCAPIKey(apiKey string, requireTransportSecurity bool) *gRPCApiKey {
	return &gRPCApiKey{apiKey: apiKey, requireTransportSecurity: requireTransportSecurity}
}

func (a *gRPCApiKey) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"X-API-key": a.apiKey}, nil
}

func (a *gRPCApiKey) RequireTransportSecurity() bool {
	return a.requireTransportSecurity
}
