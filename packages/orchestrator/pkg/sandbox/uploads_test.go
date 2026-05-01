package sandbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blockmocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/mocks"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	templatemocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/mocks"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type fakeCache struct {
	mu sync.Mutex
	m  map[string]template.Template
}

func newFakeCache() *fakeCache {
	return &fakeCache{m: make(map[string]template.Template)}
}

func (f *fakeCache) GetCachedTemplate(buildID string) (template.Template, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.m[buildID]

	return t, ok
}

func (f *fakeCache) put(buildID string, tpl template.Template) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[buildID] = tpl
}

func newUploads(t *testing.T) (*Uploads, *fakeCache) {
	t.Helper()
	cache := newFakeCache()
	futures := ttlcache.New(
		ttlcache.WithTTL[uuid.UUID, *utils.ErrorOnce](futureTTL),
	)
	go futures.Start()
	t.Cleanup(futures.Stop)

	return &Uploads{
		tc:      cache,
		futures: futures,
	}, cache
}

func putFinalHeader(t *testing.T, cache *fakeCache, buildID uuid.UUID, fileType build.DiffType) {
	t.Helper()
	tpl := templatemocks.NewMockTemplate(t)
	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(&headers.Header{
		Metadata: &headers.Metadata{Version: headers.MetadataVersionV4},
		Builds:   map[uuid.UUID]headers.BuildData{buildID: {}}, // self-entry → not stale
	}).Maybe()

	switch fileType {
	case build.Memfile:
		tpl.EXPECT().Memfile(mock.Anything).Return(dev, nil).Maybe()
	case build.Rootfs:
		tpl.EXPECT().Rootfs().Return(dev, nil).Maybe()
	}

	cache.put(buildID.String(), tpl)
}

func TestUploads_BeginDistinctIDsAreIndependent(t *testing.T) {
	t.Parallel()
	c, _ := newUploads(t)

	a := uuid.New()
	b := uuid.New()

	futA, err := c.Start(a)
	require.NoError(t, err)
	futB, err := c.Start(b)
	require.NoError(t, err)

	require.NotSame(t, futA, futB)
	require.NoError(t, futA.SetSuccess())

	select {
	case <-futB.Done():
		t.Fatal("futB should not be done after only futA fires")
	default:
	}
}

func TestUploads_Wait_BlocksUntilSet(t *testing.T) {
	t.Parallel()
	c, cache := newUploads(t)

	id := uuid.New()
	putFinalHeader(t, cache, id, build.Memfile)
	fut, err := c.Start(id)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		_, _ = c.Wait(context.Background(), id, build.Memfile)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Wait should block until the future fires")
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, fut.SetSuccess())

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait should return after future fires")
	}
}

func TestUploads_Wait_PropagatesUploadError(t *testing.T) {
	t.Parallel()
	c, cache := newUploads(t)

	id := uuid.New()
	putFinalHeader(t, cache, id, build.Memfile)
	fut, err := c.Start(id)
	require.NoError(t, err)

	uploadErr := errors.New("upload exploded")
	require.NoError(t, fut.SetError(uploadErr))

	_, err = c.Wait(context.Background(), id, build.Memfile)
	require.ErrorIs(t, err, uploadErr)
}

func TestUploads_Wait_ContextCancellation(t *testing.T) {
	t.Parallel()
	c, _ := newUploads(t)

	id := uuid.New()
	_, err := c.Start(id) // never signaled
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := c.Wait(ctx, id, build.Memfile)
		errCh <- err
	}()

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Wait should return on context cancel")
	}
}

func TestUploads_Wait_NotInCache(t *testing.T) {
	t.Parallel()
	c, _ := newUploads(t)

	id := uuid.New()
	_, err := c.Wait(context.Background(), id, build.Memfile)
	require.ErrorIs(t, err, ErrBuildNotInCache)
}

func TestUploads_Wait_NoFuture_ReadsFromCache(t *testing.T) {
	t.Parallel()
	c, cache := newUploads(t)

	id := uuid.New()
	want := &headers.Header{
		Metadata: &headers.Metadata{Version: headers.MetadataVersionV4},
		Builds:   map[uuid.UUID]headers.BuildData{id: {}},
	}

	tpl := templatemocks.NewMockTemplate(t)
	dev := blockmocks.NewMockReadonlyDevice(t)
	dev.EXPECT().Header().Return(want)
	tpl.EXPECT().Rootfs().Return(dev, nil)
	cache.put(id.String(), tpl)

	got, err := c.Wait(context.Background(), id, build.Rootfs)
	require.NoError(t, err)
	require.Same(t, want, got)
}

func TestUploads_ConcurrentBeginsAndWaits(t *testing.T) {
	t.Parallel()
	c, cache := newUploads(t)

	const n = 10

	ids := make([]uuid.UUID, n)
	futs := make([]*utils.ErrorOnce, n)
	for i := range n {
		ids[i] = uuid.New()
		putFinalHeader(t, cache, ids[i], build.Memfile)
		fut, err := c.Start(ids[i])
		require.NoError(t, err)
		futs[i] = fut
	}

	var done atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := c.Wait(context.Background(), ids[i], build.Memfile); err == nil {
				done.Add(1)
			}
		}(i)
	}

	for i := range n {
		require.NoError(t, futs[i].SetSuccess())
	}

	wg.Wait()
	assert.Equal(t, int32(n), done.Load())
}
