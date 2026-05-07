package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLastSnapshotMap(t *testing.T) {
	m := newLastSnapshotMap()

	_, ok := m.Get("sbx-a")
	assert.False(t, ok, "empty map: expected miss")

	m.Set("sbx-a", "build-1")
	got, ok := m.Get("sbx-a")
	assert.True(t, ok)
	assert.Equal(t, "build-1", got.BuildID)

	// Set is last-write-wins.
	m.Set("sbx-a", "build-2")
	got, ok = m.Get("sbx-a")
	assert.True(t, ok)
	assert.Equal(t, "build-2", got.BuildID)

	// Independent keys don't bleed.
	m.Set("sbx-b", "build-3")
	got, _ = m.Get("sbx-a")
	assert.Equal(t, "build-2", got.BuildID, "sbx-a should not be clobbered by sbx-b's set")

	m.Delete("sbx-a")
	_, ok = m.Get("sbx-a")
	assert.False(t, ok, "delete should remove the entry")

	// Deleting a missing key is a no-op (no panic).
	m.Delete("never-set")
}
