package logs

import (
	"context"

	"github.com/google/uuid"
)

type Provider interface {
	GetLogs(ctx context.Context, templateID string, buildUUID uuid.UUID, clusterID *uuid.UUID, clusterNodeID *string, offset *int32) ([]string, error)
}

type SkippedProviderError struct{}

func (e *SkippedProviderError) Error() string {
	return "skipped provider"
}
