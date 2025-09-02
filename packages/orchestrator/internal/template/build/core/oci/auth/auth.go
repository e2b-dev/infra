package auth

import (
	"context"

	"github.com/google/go-containerregistry/pkg/v1/remote"

	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

// RegistryAuthProvider is an interface for different registry authentication providers
type RegistryAuthProvider interface {
	// GetAuthOption returns the remote.Option for authentication
	GetAuthOption(ctx context.Context) (remote.Option, error)
}

// NewAuthProvider is a factory function that creates the appropriate auth provider
func NewAuthProvider(registry *templatemanager.FromImageRegistry) RegistryAuthProvider {
	if registry == nil {
		return nil
	}

	switch auth := registry.Type.(type) {
	case *templatemanager.FromImageRegistry_Aws:
		return NewAWSAuthProvider(auth.Aws)
	case *templatemanager.FromImageRegistry_Gcp:
		return NewGCPAuthProvider(auth.Gcp)
	case *templatemanager.FromImageRegistry_General:
		return NewGeneralAuthProvider(auth.General)
	default:
		return nil
	}
}
