package deadnode

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Node liveness is a cross-replica signal stored in Redis: every API replica
// refreshes a per-node "last seen" key after each successful sync cycle with
// that node. The dead-node sweep only considers a node dead when NO replica
// has seen it for the grace period — a single replica with a broken network
// view can never purge sandboxes of a node other replicas still reach.
//
// For nodes without a last-seen key (fresh feature rollout, or the node died
// before any replica ever synced it), a first-missing marker is written
// once (SET NX) so the grace period ages from the first observation and is
// shared across replicas and their restarts.
const (
	nodeLastSeenKeyPrefix     = "node:last-seen:"
	nodeFirstMissingKeyPrefix = "node:first-missing:"

	// nodeLivenessKeyTTL self-cleans keys of nodes that were removed from the
	// fleet permanently.
	nodeLivenessKeyTTL = 24 * time.Hour
)

// NodeRef identifies a node across clusters. Comparable — used as a map key.
type NodeRef struct {
	ClusterID string
	NodeID    string
}

// nodeLiveness is the cross-replica view of one node.
type nodeLiveness struct {
	// lastSeen is the last time any replica completed a successful sync with
	// the node. Zero when no replica has ever recorded seeing it.
	lastSeen time.Time
	// firstMissing is when a replica first observed the node having no
	// last-seen key. Only meaningful when lastSeen is zero.
	firstMissing time.Time
}

func nodeLastSeenKey(ref NodeRef) string {
	return nodeLastSeenKeyPrefix + ref.ClusterID + ":" + ref.NodeID
}

func nodeFirstMissingKey(ref NodeRef) string {
	return nodeFirstMissingKeyPrefix + ref.ClusterID + ":" + ref.NodeID
}

// RecordNodeSeen refreshes the node's last-seen key and clears any
// first-missing marker. Called by every replica after a successful sync
// cycle unconditionally
func RecordNodeSeen(ctx context.Context, client redis.UniversalClient, ref NodeRef, now time.Time) error {
	pipe := client.Pipeline()
	pipe.Set(ctx, nodeLastSeenKey(ref), strconv.FormatInt(now.Unix(), 10), nodeLivenessKeyTTL)
	pipe.Del(ctx, nodeFirstMissingKey(ref))
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to record node seen: %w", err)
	}

	return nil
}

// fetchNodeLiveness returns the liveness for the given nodes. For nodes
// without a last-seen key it plants (SET NX) and reads back the shared
// first-missing marker so the missing-grace ages consistently across replicas
func fetchNodeLiveness(ctx context.Context, client redis.UniversalClient, refs []NodeRef, now time.Time) (map[NodeRef]nodeLiveness, error) {
	if len(refs) == 0 {
		return map[NodeRef]nodeLiveness{}, nil
	}

	pipe := client.Pipeline()
	lastSeenCmds := make([]*redis.StringCmd, len(refs))
	for i, ref := range refs {
		lastSeenCmds[i] = pipe.Get(ctx, nodeLastSeenKey(ref))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to fetch node last-seen keys: %w", err)
	}

	out := make(map[NodeRef]nodeLiveness, len(refs))
	var missing []NodeRef
	for i, ref := range refs {
		ts, err := parseUnixSeconds(lastSeenCmds[i])
		if err != nil {
			missing = append(missing, ref)

			continue
		}

		out[ref] = nodeLiveness{lastSeen: ts}
	}

	if len(missing) == 0 {
		return out, nil
	}

	// Plant the shared first-missing marker (first writer wins) and read the
	// authoritative value back in one pipeline.
	markerPipe := client.Pipeline()
	markerCmds := make([]*redis.StringCmd, len(missing))
	for i, ref := range missing {
		markerPipe.SetNX(ctx, nodeFirstMissingKey(ref), strconv.FormatInt(now.Unix(), 10), nodeLivenessKeyTTL)
		markerCmds[i] = markerPipe.Get(ctx, nodeFirstMissingKey(ref))
	}
	if _, err := markerPipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to plant node first-missing markers: %w", err)
	}

	for i, ref := range missing {
		ts, err := parseUnixSeconds(markerCmds[i])
		if err != nil {
			// Leave the node without data; the sweep skips nodes it has no evidence about
			logger.L().Warn(ctx, "Failed to read node first-missing marker",
				zap.Error(err),
				logger.WithNodeID(ref.NodeID),
			)

			continue
		}

		out[ref] = nodeLiveness{firstMissing: ts}
	}

	return out, nil
}

func parseUnixSeconds(cmd *redis.StringCmd) (time.Time, error) {
	raw, err := cmd.Result()
	if err != nil {
		return time.Time{}, err
	}

	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid unix timestamp %q: %w", raw, err)
	}

	return time.Unix(sec, 0), nil
}
