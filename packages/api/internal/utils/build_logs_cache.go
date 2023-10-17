package utils

import (
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

const (
	logsExpiration = time.Second * 60 * 5 // 5 minutes
)

type logIdentifier struct {
	envID   string
	buildID string
}

type BuildLogsCache struct {
	cache *ttlcache.Cache[logIdentifier, []string]
	mutex sync.RWMutex
}

func NewBuildLogsCache() *BuildLogsCache {
	return &BuildLogsCache{
		cache: ttlcache.New(ttlcache.WithTTL[logIdentifier, []string](logsExpiration)),
		mutex: sync.RWMutex{},
	}
}

func (c *BuildLogsCache) Get(envID, buildID string) ([]string, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	key := &logIdentifier{envID: envID, buildID: buildID}
	item := c.cache.Get(*key).Value()

	if item != nil {
		return item, nil
	}

	return nil, fmt.Errorf("build %s for %s not found in cache", buildID, envID)
}

func (c *BuildLogsCache) Refresh(envID, buildID string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := &logIdentifier{envID: envID, buildID: buildID}

	item := c.cache.Get(*key)

	if item == nil {
		return fmt.Errorf("build %s for %s not found in cache", buildID, envID)
	}

	return nil
}

func (c *BuildLogsCache) Set(envID, buildID string, value []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := &logIdentifier{envID: envID, buildID: buildID}
	c.cache.Set(*key, value, logsExpiration)

	return nil
}