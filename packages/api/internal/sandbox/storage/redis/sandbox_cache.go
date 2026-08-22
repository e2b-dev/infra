package redis

import (
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

// sandboxCache is an in-process read-through cache for sandbox state, keyed by
// sandbox ID and indexed by team. It is populated via sandbox state-change
// events received over the shared pub/sub channel and by cold-fetch on the
// first TeamItems call for a team.
//
// Consistency model: the cache is eventually consistent with Redis. Dropped
// pub/sub events cause temporary staleness until the next TeamItems cold-fetch
// for that team. Callers that require strong consistency (e.g. ExpiredItems,
// Reconcile) must bypass the cache and query Redis directly.
type sandboxCache struct {
	mu     sync.RWMutex
	byID   map[string]sandboxtypes.Sandbox // sandboxID → sandbox
	byTeam map[string]map[string]struct{}  // teamID → set of sandboxIDs
	warm   map[string]struct{}             // teamIDs whose cache is fully warm
}

func newSandboxCache() *sandboxCache {
	return &sandboxCache{
		byID:   make(map[string]sandboxtypes.Sandbox),
		byTeam: make(map[string]map[string]struct{}),
		warm:   make(map[string]struct{}),
	}
}

// apply updates the cache from a sandbox state-change event.
func (c *sandboxCache) apply(evt sandboxEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch evt.Op {
	case sandboxEventOpAdd, sandboxEventOpUpdate:
		if evt.Sandbox == nil {
			return
		}

		sbx := *evt.Sandbox
		teamID := sbx.TeamID.String()
		c.byID[sbx.SandboxID] = sbx

		if c.byTeam[teamID] == nil {
			c.byTeam[teamID] = make(map[string]struct{})
		}

		c.byTeam[teamID][sbx.SandboxID] = struct{}{}

	case sandboxEventOpRemove:
		c.evictLocked(evt.TeamID, evt.SandboxID)
	}
}

// evictLocked removes a sandbox entry. Must be called with c.mu held.
func (c *sandboxCache) evictLocked(teamID, sandboxID string) {
	delete(c.byID, sandboxID)

	if ids, ok := c.byTeam[teamID]; ok {
		delete(ids, sandboxID)
		if len(ids) == 0 {
			delete(c.byTeam, teamID)
		}
	}
}

// warmTeam atomically replaces the cached snapshot for teamID with the
// freshly-fetched set of sandboxes, then marks the team as warm.
//
// Sandboxes already in the cache for this team but absent from the fresh fetch
// are evicted (they were removed from Redis while the cache was cold).
func (c *sandboxCache) warmTeam(teamID string, sandboxes []sandboxtypes.Sandbox) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build lookup of the fresh set to detect removals.
	fresh := make(map[string]struct{}, len(sandboxes))
	for _, sbx := range sandboxes {
		fresh[sbx.SandboxID] = struct{}{}
	}

	// Evict stale entries that are no longer present in Redis.
	if existing, ok := c.byTeam[teamID]; ok {
		for sid := range existing {
			if _, seen := fresh[sid]; !seen {
				c.evictLocked(teamID, sid)
			}
		}
	}

	// Insert or overwrite with the fresh state.
	for _, sbx := range sandboxes {
		c.byID[sbx.SandboxID] = sbx

		if c.byTeam[teamID] == nil {
			c.byTeam[teamID] = make(map[string]struct{})
		}

		c.byTeam[teamID][sbx.SandboxID] = struct{}{}
	}

	c.warm[teamID] = struct{}{}
}

// getTeam returns the cached sandboxes for teamID filtered by states.
// Returns (nil, false) when the team has not been warmed yet; the caller
// should fall back to a Redis cold-fetch.
func (c *sandboxCache) getTeam(teamID uuid.UUID, states []sandboxtypes.State) ([]sandboxtypes.Sandbox, bool) {
	tid := teamID.String()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if _, ok := c.warm[tid]; !ok {
		return nil, false
	}

	ids, ok := c.byTeam[tid]
	if !ok {
		return []sandboxtypes.Sandbox{}, true
	}

	result := make([]sandboxtypes.Sandbox, 0, len(ids))
	for id := range ids {
		sbx, found := c.byID[id]
		if !found {
			continue
		}

		if len(states) > 0 && !slices.Contains(states, sbx.State) {
			continue
		}

		result = append(result, sbx)
	}

	return result, true
}

// isWarm reports whether the cache holds a complete snapshot for teamID.
// Used only in tests to assert cold-start and warm-path behaviour.
func (c *sandboxCache) isWarm(teamID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.warm[teamID]

	return ok
}
