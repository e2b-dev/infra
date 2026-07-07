package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticDiscovery is a Discovery stub returning canned nodes or an error.
type staticDiscovery struct {
	nodes []Node
	err   error
}

func (d *staticDiscovery) ListNodes(context.Context) ([]Node, error) {
	if d.err != nil {
		return nil, d.err
	}

	return d.nodes, nil
}

// The primary (service-based) entry must win on conflict: it carries the real
// bound port. Dedupe itself is lo.UniqBy's job; this pins the concatenation
// order that makes primary win.
func TestMergedDiscovery_PrimaryWinsOnConflict(t *testing.T) {
	t.Parallel()

	primary := &staticDiscovery{nodes: []Node{
		{ShortID: "aaaaaaaa", IPAddress: "10.0.0.1", OrchestratorAddress: "10.0.0.1:6123"},
	}}
	fallback := &staticDiscovery{nodes: []Node{
		{ShortID: "aaaaaaaa", IPAddress: "10.0.0.1", OrchestratorAddress: "10.0.0.1:5008"},
	}}

	d := NewMerged(primary, fallback)
	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	assert.Equal(t, "aaaaaaaa", nodes[0].ShortID)
	assert.Equal(t, "10.0.0.1:6123", nodes[0].OrchestratorAddress, "primary (service-based) entry must win on conflict")
}

// A primary failure fails the whole listing.
func TestMergedDiscovery_PrimaryError(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("nomad agent unreachable")
	primary := &staticDiscovery{err: primaryErr}
	fallback := &staticDiscovery{nodes: []Node{
		{ShortID: "bbbbbbbb", IPAddress: "10.0.0.2", OrchestratorAddress: "10.0.0.2:5008"},
	}}

	d := NewMerged(primary, fallback)
	nodes, err := d.ListNodes(t.Context())
	require.ErrorIs(t, err, primaryErr)
	assert.Nil(t, nodes)
}

// A fallback failure also fails the whole listing: no silent degradation to a
// partial node list.
func TestMergedDiscovery_FallbackError(t *testing.T) {
	t.Parallel()

	fallbackErr := errors.New("nomad agent unreachable")
	primary := &staticDiscovery{nodes: []Node{
		{ShortID: "aaaaaaaa", IPAddress: "10.0.0.1", OrchestratorAddress: "10.0.0.1:5008"},
	}}
	fallback := &staticDiscovery{err: fallbackErr}

	d := NewMerged(primary, fallback)
	nodes, err := d.ListNodes(t.Context())
	require.ErrorIs(t, err, fallbackErr)
	assert.Nil(t, nodes)
}
