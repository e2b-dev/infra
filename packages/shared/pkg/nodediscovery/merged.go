package nodediscovery

import (
	"context"
	"fmt"
	"slices"

	"github.com/samber/lo"
)

// mergedDiscovery unions a primary and a fallback Discovery, deduplicated by
// ID with the primary entry winning on conflict. It bridges the migration from
// node-pool-based to service-based discovery: the service registration carries
// the real bound port, so it wins when both backends report the same node.
//
// ListInstances fails if either backend fails: the caller treats a discovery
// error as "skip this cycle", which beats silently acting on a partial list.
// A cached backend cannot fail this way (see the package doc), so beneath one
// the same union degrades to stale entries instead.
type mergedDiscovery struct {
	primary  Discovery
	fallback Discovery
}

// NewMerged creates a Discovery that unions primary's and fallback's
// instances, deduplicated by ID with primary taking precedence.
func NewMerged(primary, fallback Discovery) Discovery {
	return &mergedDiscovery{
		primary:  primary,
		fallback: fallback,
	}
}

func (d *mergedDiscovery) ListInstances(ctx context.Context) ([]Instance, error) {
	ctx, span := tracer.Start(ctx, "list-merged-nodes")
	defer span.End()

	primaryInstances, err := d.primary.ListInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("primary discovery failed: %w", err)
	}

	fallbackInstances, err := d.fallback.ListInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallback discovery failed: %w", err)
	}

	// UniqBy keeps the first occurrence per key, so primary entries shadow
	// fallback entries with the same ID.
	return lo.UniqBy(slices.Concat(primaryInstances, fallbackInstances), func(i Instance) string {
		return i.ID
	}), nil
}

func (d *mergedDiscovery) Start(ctx context.Context) {
	d.primary.Start(ctx)
	d.fallback.Start(ctx)
}

func (d *mergedDiscovery) Stop(ctx context.Context) {
	d.primary.Stop(ctx)
	d.fallback.Stop(ctx)
}
