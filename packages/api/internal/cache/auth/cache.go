package autchcache

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	authInfoExpiration = 5 * time.Minute
	refreshInterval    = 1 * time.Minute
	refreshTimeout     = 30 * time.Second
)

type DataCallback = func(ctx context.Context, key string) (*types.Team, error)

type TeamAuthCache struct {
	cache *cache.Cache[string, *types.Team]
}

func NewTeamAuthCache() *TeamAuthCache {
	config := cache.Config{
		TTL:             authInfoExpiration,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  refreshTimeout,
	}

	return &TeamAuthCache{
		cache: cache.NewCache[string, *types.Team](config),
	}
}

// TODO: save blocked teams to cache as well, handle the condition in the GetOrSet method
func (c *TeamAuthCache) GetOrSet(ctx context.Context, key string, dataCallback DataCallback) (team *types.Team, err error) {
	team, err = c.cache.GetOrSet(ctx, key, dataCallback)
	if err != nil {
		return nil, fmt.Errorf("error while getting the team: %w", err)
	}

	return team, nil
}
