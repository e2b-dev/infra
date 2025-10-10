package autchcache

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
)

const (
	authInfoExpiration = 5 * time.Minute
	refreshInterval    = 1 * time.Minute
)

type TeamInfo struct {
	team *types.Team

	lastRefresh time.Time
	once        singleflight.Group
}

type DataCallback = func(ctx context.Context, key string) (*types.Team, error)

type TeamAuthCache struct {
	cache *ttlcache.Cache[string, *TeamInfo]
}

func NewTeamAuthCache() *TeamAuthCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *TeamInfo](authInfoExpiration))
	go cache.Start()

	return &TeamAuthCache{
		cache: cache,
	}
}

// TODO: save blocked teams to cache as well, handle the condition in the GetOrSet method
func (c *TeamAuthCache) GetOrSet(ctx context.Context, key string, dataCallback DataCallback) (team *types.Team, err error) {
	var item *ttlcache.Item[string, *TeamInfo]
	var templateInfo *TeamInfo

	item = c.cache.Get(key)
	if item == nil {
		team, err = dataCallback(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("error while getting the team: %w", err)
		}

		templateInfo = &TeamInfo{team: team, lastRefresh: time.Now()}
		c.cache.Set(key, templateInfo, authInfoExpiration)

		return team, nil
	}

	templateInfo = item.Value()
	if time.Since(templateInfo.lastRefresh) > refreshInterval {
		go templateInfo.once.Do(key, func() (any, error) { //nolint:contextcheck // TODO: fix this later
			c.Refresh(key, dataCallback)
			return nil, err
		})
	}

	return templateInfo.team, nil
}

// Refresh refreshes the cache for the given team ID.
func (c *TeamAuthCache) Refresh(key string, dataCallback DataCallback) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	team, err := dataCallback(ctx, key)
	if err != nil {
		c.cache.Delete(key)

		return
	}

	c.cache.Set(key, &TeamInfo{team: team, lastRefresh: time.Now()}, authInfoExpiration)
}
