package analyticscollector

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type gRPCApiKey struct {
	apiKey string
}

func newGRPCAPIKey(apiKey string) *gRPCApiKey {
	return &gRPCApiKey{apiKey: apiKey}
}

func (a *gRPCApiKey) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"X-API-key": a.apiKey}, nil
}

func (a *gRPCApiKey) RequireTransportSecurity() bool {
	return !env.IsLocal()
}
