//go:build linux

package service

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkOwnershipHandoff(t *testing.T) {
	t.Parallel()

	info := &ServiceInfo{}
	parentDone := info.TrackWork()
	childDone := info.TrackWork()
	require.Equal(t, int64(2), info.OutstandingWork())
	parentDone()
	require.Equal(t, int64(1), info.OutstandingWork())
	childDone()
	require.Zero(t, info.OutstandingWork())
}

func TestConcurrentWorkOwnership(t *testing.T) {
	t.Parallel()

	info := &ServiceInfo{}
	var started, finished sync.WaitGroup
	release := make(chan struct{})
	for range 32 {
		started.Add(1)
		finished.Go(func() {
			done := info.TrackWork()
			defer done()
			started.Done()
			<-release
		})
	}
	started.Wait()
	require.Equal(t, int64(32), info.OutstandingWork())
	close(release)
	finished.Wait()
	require.Zero(t, info.OutstandingWork())
}
