package auth

import (
	"context"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

// GeneralAuthProvider implements authentication for registries with username/password
type GeneralAuthProvider struct {
	registry *templatemanager.GeneralRegistry
}

// NewGeneralAuthProvider creates a new general auth provider
func NewGeneralAuthProvider(registry *templatemanager.GeneralRegistry) *GeneralAuthProvider {
	return &GeneralAuthProvider{
		registry: registry,
	}
}

// GetAuthOption returns the authentication option for general registries
func (p *GeneralAuthProvider) GetAuthOption(ctx context.Context) (remote.Option, error) {
	return remote.WithAuth(&authn.Basic{
		Username: p.registry.Username,
		Password: p.registry.Password,
	}), nil
}
