package autchcache

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	authInfoExpiration = 5 * time.Minute
	refreshInterval    = 1 * time.Minute
	refreshTimeout     = 30 * time.Second
	callbackTimeout    = 30 * time.Second
)

type DataCallback = func(ctx context.Context, key string) (*types.Team, error)

type TeamAuthCache struct {
	cache *cache.Cache[*types.Team]
}

func NewTeamAuthCache() *TeamAuthCache {
	config := cache.Config[*types.Team]{
		TTL:             authInfoExpiration,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  refreshTimeout,
		CallbackTimeout: callbackTimeout,
	}

	return &TeamAuthCache{
		cache: cache.NewCache[*types.Team](config),
	}
}

// TODO: save blocked teams to cache as well, handle the condition in the GetOrSet method
func (c *TeamAuthCache) GetOrSet(ctx context.Context, key string, dataCallback DataCallback) (team *types.Team, err error) {
	team, err = c.cache.GetOrSet(ctx, key, dataCallback)
	if err != nil {
		return nil, err
	}

	return team, nil
}

func (c *TeamAuthCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}
