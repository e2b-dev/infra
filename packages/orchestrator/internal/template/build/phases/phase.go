package phases

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

type BuilderPhase interface {
	Build(ctx context.Context, lastStepResult LayerResult) (LayerResult, error)
}

type LayerResult struct {
	Metadata cache.LayerMetadata
	Cached   bool
	Hash     string

	StartMetadata *metadata.StartMetadata
}

func (l LayerResult) Next(next LayerResult) LayerResult {
	newHash := cache.HashKeys(l.Hash, next.Hash)

	return LayerResult{
		Metadata: next.Metadata,
		Cached:   next.Cached,
		Hash:     newHash,
	}
}

func LayerInfo(
	cached bool,
	prefix string,
	text string,
	hash string,
) string {
	cachedPrefix := ""
	if cached {
		cachedPrefix = "CACHED "
	}
	return fmt.Sprintf("%s[%s] %s [%s]", cachedPrefix, prefix, text, hash)
}
