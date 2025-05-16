package autchcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

const authInfoExpiration = 5 * time.Minute
const refreshInterval = 1 * time.Minute

type AuthTeamInfo struct {
	Team *models.Team
	Tier *models.Tier
}

type TeamInfo struct {
	team *models.Team
	tier *models.Tier

	lastRefresh time.Time
	once        singleflight.Group
	lock        sync.Mutex
}

type TeamAuthCache struct {
	cache *ttlcache.Cache[string, *TeamInfo]
	db    *db.DB
}

func NewTeamAuthCache(db *db.DB) *TeamAuthCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *TeamInfo](authInfoExpiration))
	go cache.Start()

	return &TeamAuthCache{
		cache: cache,
		db:    db,
	}
}

// TODO: save blocked teams to cache as well, handle the condition in the Get method
func (c *TeamAuthCache) Get(ctx context.Context, apiKey string) (team *models.Team, tier *models.Tier, err error) {
	var item *ttlcache.Item[string, *TeamInfo]
	var templateInfo *TeamInfo

	item = c.cache.Get(apiKey)
	if item == nil {
		team, tier, err = c.db.GetTeamAuth(ctx, apiKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get the team from db for an api key: %w", err)
		}

		templateInfo = &TeamInfo{team: team, tier: tier, lastRefresh: time.Now()}
		c.cache.Set(apiKey, templateInfo, authInfoExpiration)

		return team, tier, nil
	}

	templateInfo = item.Value()
	if time.Since(templateInfo.lastRefresh) > refreshInterval {
		go templateInfo.once.Do(apiKey, func() (interface{}, error) {
			c.Refresh(apiKey)
			return nil, err
		})
	}

	return templateInfo.team, templateInfo.tier, nil
}

// Refresh refreshes the cache for the given team ID.
func (c *TeamAuthCache) Refresh(apiKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	team, tier, err := c.db.GetTeamAuth(ctx, apiKey)
	if err != nil {
		c.cache.Delete(apiKey)

		return
	}

	c.cache.Set(apiKey, &TeamInfo{team: team, tier: tier, lastRefresh: time.Now()}, authInfoExpiration)
}
