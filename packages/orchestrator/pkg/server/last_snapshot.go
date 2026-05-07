// last_snapshot.go — best-effort in-memory record of each sandbox's
// most recent published checkpoint BuildID. Consumed by the
// skip_if_unchanged short-circuit in Server.Checkpoint (issue #2580).
//
// The map is intentionally not persisted: an orchestrator restart
// simply forces the next skip-attempt to fall through to a full
// checkpoint, which is a no-op vs. the historical behavior.

package server

import "sync"

type lastSnapshot struct {
	BuildID string
}

type lastSnapshotMap struct {
	mu      sync.RWMutex
	entries map[string]lastSnapshot
}

func newLastSnapshotMap() *lastSnapshotMap {
	return &lastSnapshotMap{entries: map[string]lastSnapshot{}}
}

func (m *lastSnapshotMap) Get(sandboxID string) (lastSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.entries[sandboxID]
	return v, ok
}

func (m *lastSnapshotMap) Set(sandboxID, buildID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[sandboxID] = lastSnapshot{BuildID: buildID}
}

func (m *lastSnapshotMap) Delete(sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, sandboxID)
}
