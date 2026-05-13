package snapshotcache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	snapshotCacheTTL             = 5 * time.Minute
	snapshotCacheRefreshInterval = 1 * time.Minute

	snapshotCacheKeyPrefix = "snapshot:last"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/cache/snapshots")

// SnapshotInfo holds cached snapshot with build and alias information.
type SnapshotInfo struct {
	Aliases  []string         `json:"aliases"`
	Names    []string         `json:"names"`
	Snapshot queries.Snapshot `json:"snapshot"`
	EnvBuild queries.EnvBuild `json:"env_build"`
	NotFound bool             `json:"not_found,omitempty"`
}

var errNotFoundSentinel = &SnapshotInfo{NotFound: true}

var ErrSnapshotNotFound = errors.New("snapshot not found")

type SnapshotCache struct {
	cache *cache.RedisCache[*SnapshotInfo]
	db    *sqlcdb.Client
}

func NewSnapshotCache(db *sqlcdb.Client, redisClient redis.UniversalClient) *SnapshotCache {
	rc := cache.NewRedisCache(cache.RedisConfig[*SnapshotInfo]{
		TTL:             snapshotCacheTTL,
		RefreshInterval: snapshotCacheRefreshInterval,
		RedisClient:     redisClient,
		RedisPrefix:     snapshotCacheKeyPrefix,
	})

	return &SnapshotCache{
		cache: rc,
		db:    db,
	}
}

// Get returns the last snapshot for a sandbox, using cache with DB fallback.
func (c *SnapshotCache) Get(ctx context.Context, sandboxID string) (*SnapshotInfo, error) {
	ctx, span := tracer.Start(ctx, "get last snapshot")
	defer span.End()

	info, err := c.cache.GetOrSet(ctx, sandboxID, c.fetchFromDB)
	if err != nil {
		return nil, err
	}

	if info.NotFound {
		return nil, ErrSnapshotNotFound
	}

	return info, nil
}

// GetByTeam returns the last snapshot for a sandbox scoped to a specific team.
// It uses the cache for the initial lookup and validates team ownership at the DB level,
// avoiding a separate post-fetch ownership check.
func (c *SnapshotCache) GetByTeam(ctx context.Context, sandboxID string, teamID uuid.UUID) (*SnapshotInfo, error) {
	ctx, span := tracer.Start(ctx, "get last snapshot by team")
	defer span.End()

	// Try cache first; if the cached entry belongs to the right team, return it directly.
	info, err := c.cache.GetOrSet(ctx, sandboxID, c.fetchFromDB)
	if err != nil {
		return nil, err
	}

	if info.NotFound {
		return nil, ErrSnapshotNotFound
	}

	// Cache hit and team matches – fast path.
	if info.Snapshot.TeamID == teamID {
		return info, nil
	}

	// Cache hit but team mismatch: the cached entry may belong to a different team.
	// Fall back to a team-scoped DB query to get the authoritative answer.
	row, err := c.db.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			return nil, ErrSnapshotNotFound
		}

		return nil, fmt.Errorf("fetching last snapshot by team: %w", err)
	}

	return &SnapshotInfo{
		Aliases:  row.Aliases,
		Names:    row.Names,
		Snapshot: row.Snapshot,
		EnvBuild: row.EnvBuild,
	}, nil
}

func (c *SnapshotCache) fetchFromDB(ctx context.Context, sandboxID string) (*SnapshotInfo, error) {
	ctx, span := tracer.Start(ctx, "fetch last snapshot from DB")
	defer span.End()

	row, err := c.db.GetLastSnapshot(ctx, sandboxID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			return errNotFoundSentinel, nil
		}

		return nil, fmt.Errorf("fetching last snapshot: %w", err)
	}

	return &SnapshotInfo{
		Aliases:  row.Aliases,
		Names:    row.Names,
		Snapshot: row.Snapshot,
		EnvBuild: row.EnvBuild,
	}, nil
}

// Invalidate removes the cached snapshot for a sandbox.
func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string) {
	c.cache.Delete(ctx, sandboxID)
}

func (c *SnapshotCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
