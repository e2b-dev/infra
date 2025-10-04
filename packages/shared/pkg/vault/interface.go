package vault

import (
	"context"
)

type VaultBackend interface {
	GetSecret(ctx context.Context, path string) (string, map[string]any, error)

	WriteSecret(ctx context.Context, path string, value string, metadata map[string]any) error
}
