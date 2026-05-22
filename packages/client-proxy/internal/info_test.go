package internal

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceInfo_DefaultStatusIsZeroValue(t *testing.T) {
	t.Parallel()

	s := &ServiceInfo{}
	require.Equal(t, ServiceHealth(""), s.GetStatus())
}

func TestServiceInfo_SetAndGetStatus(t *testing.T) {
	t.Parallel()

	s := &ServiceInfo{}
	ctx := t.Context()

	s.SetStatus(ctx, Healthy)
	require.Equal(t, Healthy, s.GetStatus())

	s.SetStatus(ctx, Draining)
	require.Equal(t, Draining, s.GetStatus())

	s.SetStatus(ctx, Unhealthy)
	require.Equal(t, Unhealthy, s.GetStatus())
}

func TestServiceInfo_SetSameStatusIsIdempotent(t *testing.T) {
	t.Parallel()

	s := &ServiceInfo{}
	ctx := t.Context()

	s.SetStatus(ctx, Healthy)
	s.SetStatus(ctx, Healthy)
	require.Equal(t, Healthy, s.GetStatus())
}

func TestServiceInfo_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	s := &ServiceInfo{}
	ctx := t.Context()
	statuses := []ServiceHealth{Healthy, Draining, Unhealthy}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			s.SetStatus(ctx, statuses[idx%len(statuses)])
		}(i)
		go func() {
			defer wg.Done()
			_ = s.GetStatus()
		}()
	}
	wg.Wait()

	require.Contains(t, statuses, s.GetStatus())
}
