package vault

import (
	"context"
)

// If you self-host E2B and don't want to use Hashicorp Vault, you can implement this interface to use your own vault backend
type VaultBackend interface {
	GetSecret(ctx context.Context, path string) (string, map[string]any, error)

	WriteSecret(ctx context.Context, path string, value string, metadata map[string]any) error

	DeleteSecret(ctx context.Context, path string) error

	Close()
}
