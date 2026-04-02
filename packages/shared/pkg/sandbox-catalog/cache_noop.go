package sandbox_catalog

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type NoopSandboxCache struct{}

var _ SandboxCache = (*NoopSandboxCache)(nil)

func NewNoopSandboxCache() *NoopSandboxCache {
	return &NoopSandboxCache{}
}

func (n *NoopSandboxCache) Get(string, ...ttlcache.Option[string, *SandboxInfo]) *ttlcache.Item[string, *SandboxInfo] {
	return nil
}

func (n *NoopSandboxCache) Set(string, *SandboxInfo, time.Duration) *ttlcache.Item[string, *SandboxInfo] {
	return nil
}

func (n *NoopSandboxCache) Delete(string) {}

func (n *NoopSandboxCache) Stop() {}
