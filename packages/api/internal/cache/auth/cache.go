package autchcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/db/queries"
)

const (
	authInfoExpiration = 5 * time.Minute
	refreshInterval    = 1 * time.Minute
)

type AuthTeamInfo struct {
	Team *queries.Team
	Tier *queries.Tier
}

type TeamInfo struct {
	team *queries.Team
	tier *queries.Tier

	lastRefresh time.Time
	once        singleflight.Group
	lock        sync.Mutex
}

type DataCallback = func(ctx context.Context, key string) (*queries.Team, *queries.Tier, error)

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
func (c *TeamAuthCache) GetOrSet(ctx context.Context, key string, dataCallback DataCallback) (team *queries.Team, tier *queries.Tier, err error) {
	var item *ttlcache.Item[string, *TeamInfo]
	var templateInfo *TeamInfo

	item = c.cache.Get(key)
	if item == nil {
		team, tier, err = dataCallback(ctx, key)
		if err != nil {
			return nil, nil, fmt.Errorf("error while getting the team: %w", err)
		}

		templateInfo = &TeamInfo{team: team, tier: tier, lastRefresh: time.Now()}
		c.cache.Set(key, templateInfo, authInfoExpiration)

		return team, tier, nil
	}

	templateInfo = item.Value()
	if time.Since(templateInfo.lastRefresh) > refreshInterval {
		go templateInfo.once.Do(key, func() (interface{}, error) {
			c.Refresh(key, dataCallback)
			return nil, err
		})
	}

	return templateInfo.team, templateInfo.tier, nil
}

// Refresh refreshes the cache for the given team ID.
func (c *TeamAuthCache) Refresh(key string, dataCallback DataCallback) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	team, tier, err := dataCallback(ctx, key)
	if err != nil {
		c.cache.Delete(key)

		return
	}

	c.cache.Set(key, &TeamInfo{team: team, tier: tier, lastRefresh: time.Now()}, authInfoExpiration)
}
