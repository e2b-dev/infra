package auth

import (
	"context"

	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

// GCPAuthProvider implements authentication for Google Container Registry
type GCPAuthProvider struct {
	registry *templatemanager.GCPRegistry
}

// NewGCPAuthProvider creates a new GCP auth provider
func NewGCPAuthProvider(registry *templatemanager.GCPRegistry) *GCPAuthProvider {
	return &GCPAuthProvider{
		registry: registry,
	}
}

// GetAuthOption returns the authentication option for GCP
func (p *GCPAuthProvider) GetAuthOption(ctx context.Context) (remote.Option, error) {
	// Create authenticator using the service account JSON
	authenticator := google.NewJSONKeyAuthenticator(p.registry.ServiceAccountJson)
	return remote.WithAuth(authenticator), nil
}
