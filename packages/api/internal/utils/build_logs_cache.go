package utils

import (
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

const (
	logsExpiration = time.Second * 60 * 5 // 5 minutes
)

type BuildLogs struct {
	BuildID string
	TeamID  string
	Status  api.EnvironmentBuildStatus
	Logs    []string
}

type BuildLogsCache struct {
	cache *ttlcache.Cache[string, BuildLogs]
	mutex sync.RWMutex
}

func NewBuildLogsCache() *BuildLogsCache {
	return &BuildLogsCache{
		cache: ttlcache.New(ttlcache.WithTTL[string, BuildLogs](logsExpiration)),
		mutex: sync.RWMutex{},
	}
}

func (c *BuildLogsCache) Get(envID string, buildID string) (BuildLogs, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item := c.cache.Get(envID)

	if item != nil {
		if item.Value().BuildID != buildID {
			return BuildLogs{}, fmt.Errorf("received logs for another build %s env %s", buildID, envID)
		}
		return item.Value(), nil
	}

	return BuildLogs{}, fmt.Errorf("build for %s not found in cache", envID)
}

func (c *BuildLogsCache) Append(envID, buildID string, logs []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	item, err := c.Get(envID, buildID)
	if err != nil {
		err = fmt.Errorf("build for %s not found in cache", envID)
		return err
	}

	c.cache.Set(envID, BuildLogs{
		BuildID: item.BuildID,
		TeamID:  item.TeamID,
		Status:  item.Status,
		Logs:    append(item.Logs, logs...),
	}, logsExpiration)

	return nil
}

func (c *BuildLogsCache) Exists(envID string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	item := c.cache.Get(envID)

	return item != nil || item.Value().Status != api.EnvironmentBuildStatusBuilding
}

func (c *BuildLogsCache) Create(envID string, buildID string, teamID string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	buildLog := BuildLogs{
		BuildID: buildID,
		TeamID:  teamID,
		Status:  api.EnvironmentBuildStatusBuilding,
		Logs:    []string{},
	}
	c.cache.Set(envID, buildLog, logsExpiration)
}

func (c *BuildLogsCache) SetDone(envID string, buildID string, status api.EnvironmentBuildStatus) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	item, err := c.Get(envID, buildID)

	if err != nil {
		return fmt.Errorf("build %s not found in cache", envID)
	}

	c.cache.Set(envID, BuildLogs{
		BuildID: item.BuildID,
		Status:  status,
		Logs:    item.Logs,
		TeamID:  item.TeamID,
	}, logsExpiration)

	return nil
}
