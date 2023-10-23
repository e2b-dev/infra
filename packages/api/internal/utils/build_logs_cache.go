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

func (c *BuildLogsCache) get(envID string, buildID string) (BuildLogs, error) {
	item := c.cache.Get(envID)

	if item != nil {
		if item.Value().BuildID != buildID {
			return BuildLogs{}, fmt.Errorf("received logs for another build %s env %s", buildID, envID)
		}
		return item.Value(), nil
	}

	return BuildLogs{}, fmt.Errorf("build for %s not found in cache", envID)
}

func (c *BuildLogsCache) Get(envID string, buildID string) (BuildLogs, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.get(envID, buildID)
}

func (c *BuildLogsCache) Append(envID, buildID string, logs []string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	item, err := c.get(envID, buildID)
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

func (c *BuildLogsCache) CreateIfNotExists(teamID, envID, buildID string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	item := c.cache.Get(envID)
	if item != nil && item.Value().Status == api.EnvironmentBuildStatusBuilding {
		return fmt.Errorf("build for %s already exists in cache", envID)
	}

	buildLog := BuildLogs{
		BuildID: buildID,
		TeamID:  teamID,
		Status:  api.EnvironmentBuildStatusBuilding,
		Logs:    []string{},
	}
	c.cache.Set(envID, buildLog, logsExpiration)

	return nil
}

func (c *BuildLogsCache) Create(teamID string, envID string, buildID string) {
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

	item, err := c.get(envID, buildID)

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
