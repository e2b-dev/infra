package buildlogs

import (
	"context"

	"github.com/google/uuid"
)

type Provider interface {
	GetLogs(ctx context.Context, templateID string, buildUUID uuid.UUID, offset int) ([]string, error)
}
