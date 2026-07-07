package discovery

import (
	"context"
	"fmt"
	"slices"

	"github.com/samber/lo"
)

// mergedDiscovery unions a primary and a fallback Discovery, deduplicated by
// ShortID with the primary entry winning on conflict. It bridges the migration
// from node-pool-based to service-based discovery: the service registration
// carries the real bound port, so it wins when both backends report the same
// node.
//
// ListNodes fails if either backend fails: the caller treats a discovery
// error as "skip this cycle", which beats silently acting on a partial node
// list.
type mergedDiscovery struct {
	primary  Discovery
	fallback Discovery
}

// NewMerged creates a Discovery that unions primary's and fallback's nodes,
// deduplicated by ShortID with primary taking precedence.
func NewMerged(primary, fallback Discovery) Discovery {
	return &mergedDiscovery{
		primary:  primary,
		fallback: fallback,
	}
}

func (d *mergedDiscovery) ListNodes(ctx context.Context) ([]Node, error) {
	ctx, span := tracer.Start(ctx, "list-merged-nodes")
	defer span.End()

	primaryNodes, err := d.primary.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("primary discovery failed: %w", err)
	}

	fallbackNodes, err := d.fallback.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallback discovery failed: %w", err)
	}

	// UniqBy keeps the first occurrence per key, so primary entries shadow
	// fallback entries with the same ShortID.
	return lo.UniqBy(slices.Concat(primaryNodes, fallbackNodes), func(n Node) string {
		return n.ShortID
	}), nil
}
