package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVethObserver_AttachDetach(t *testing.T) {
	obs, err := NewVethObserver()
	require.NoError(t, err)
	defer obs.Close()

	// Attach
	err = obs.Attach("veth-1")
	assert.NoError(t, err)

	// Double attach should fail
	err = obs.Attach("veth-1")
	assert.Error(t, err)

	// Detach
	err = obs.Detach("veth-1")
	assert.NoError(t, err)

	// Double detach is idempotent
	err = obs.Detach("veth-1")
	assert.NoError(t, err)
}

func TestVethObserver_ReadCounters(t *testing.T) {
	obs, err := NewVethObserver()
	require.NoError(t, err)
	defer obs.Close()

	// Not attached → error
	_, _, err = obs.ReadCounters("veth-1")
	assert.Error(t, err)

	// Attach and read
	require.NoError(t, obs.Attach("veth-1"))
	packets, bytes, err := obs.ReadCounters("veth-1")
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), packets)
	assert.Equal(t, uint64(0), bytes)
}

func TestVethObserver_NilSafe(t *testing.T) {
	var obs *VethObserver

	assert.NoError(t, obs.Attach("veth-1"))
	assert.NoError(t, obs.Detach("veth-1"))
	assert.NoError(t, obs.Close())

	p, b, err := obs.ReadCounters("veth-1")
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), p)
	assert.Equal(t, uint64(0), b)
}

func TestVethObserver_Close(t *testing.T) {
	obs, err := NewVethObserver()
	require.NoError(t, err)

	require.NoError(t, obs.Attach("veth-1"))
	require.NoError(t, obs.Attach("veth-2"))

	err = obs.Close()
	assert.NoError(t, err)

	// After close, nothing should be attached
	obs.mu.Lock()
	assert.Equal(t, 0, len(obs.attached))
	obs.mu.Unlock()
}
