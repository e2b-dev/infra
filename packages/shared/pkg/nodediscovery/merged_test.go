package nodediscovery

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDiscovery is a Discovery stub returning canned nodes or an error.
type stubDiscovery struct {
	nodes   []Instance
	err     error
	started bool
	stopped bool
}

func (d *stubDiscovery) Start(context.Context) { d.started = true }

func (d *stubDiscovery) Stop(context.Context) { d.stopped = true }

func (d *stubDiscovery) ListInstances(context.Context) ([]Instance, error) {
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

	primary := &stubDiscovery{nodes: []Instance{
		{ID: "aaaaaaaa", IPAddress: "10.0.0.1", Port: 6123},
	}}
	fallback := &stubDiscovery{nodes: []Instance{
		{ID: "aaaaaaaa", IPAddress: "10.0.0.1", Port: 5008},
	}}

	d := NewMerged(primary, fallback)
	nodes, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	assert.Equal(t, "aaaaaaaa", nodes[0].ID)
	assert.Equal(t, "10.0.0.1:6123", nodes[0].Address(), "primary (service-based) entry must win on conflict")
}

// A primary failure fails the whole listing.
func TestMergedDiscovery_PrimaryError(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("nomad agent unreachable")
	primary := &stubDiscovery{err: primaryErr}
	fallback := &stubDiscovery{nodes: []Instance{
		{ID: "bbbbbbbb", IPAddress: "10.0.0.2", Port: 5008},
	}}

	d := NewMerged(primary, fallback)
	nodes, err := d.ListInstances(t.Context())
	require.ErrorIs(t, err, primaryErr)
	assert.Nil(t, nodes)
}

// A fallback failure also fails the whole listing: no silent degradation to a
// partial node list.
func TestMergedDiscovery_FallbackError(t *testing.T) {
	t.Parallel()

	fallbackErr := errors.New("nomad agent unreachable")
	primary := &stubDiscovery{nodes: []Instance{
		{ID: "aaaaaaaa", IPAddress: "10.0.0.1", Port: 5008},
	}}
	fallback := &stubDiscovery{err: fallbackErr}

	d := NewMerged(primary, fallback)
	nodes, err := d.ListInstances(t.Context())
	require.ErrorIs(t, err, fallbackErr)
	assert.Nil(t, nodes)
}

// The union is the one place a cached backend can sit behind a caller that
// only ever lists, so it has to pass the lifecycle through to both sides.
func TestMergedDiscovery_ForwardsStartAndStopToBothBackends(t *testing.T) {
	t.Parallel()

	primary := &stubDiscovery{}
	fallback := &stubDiscovery{}

	d := NewMerged(primary, fallback)
	d.Start(t.Context())
	d.Stop(t.Context())

	assert.True(t, primary.started)
	assert.True(t, fallback.started)
	assert.True(t, primary.stopped)
	assert.True(t, fallback.stopped)
}
