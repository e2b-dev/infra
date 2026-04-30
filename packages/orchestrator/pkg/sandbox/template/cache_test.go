package template

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func newTestCache(defaultTTL time.Duration) *Cache {
	return &Cache{
		cache: ttlcache.New(ttlcache.WithTTL[string, Template](defaultTTL)),
	}
}

func newFetchTestCache(t *testing.T) *Cache {
	t.Helper()

	return &Cache{
		cache: ttlcache.New(ttlcache.WithTTL[string, Template](time.Hour)),
		config: cfg.Config{
			BuilderConfig: cfg.BuilderConfig{
				StorageConfig: storage.Config{
					TemplateCacheDir: t.TempDir(),
				},
			},
		},
	}
}

type testTemplateFile struct {
	path string
}

func (f testTemplateFile) Path() string {
	return f.path
}

func (f testTemplateFile) Close() error {
	return nil
}

type testStorageProvider struct {
	openBlobErr error
	release     <-chan struct{}
}

func (p *testStorageProvider) wait(ctx context.Context) error {
	if p.release == nil {
		return nil
	}

	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *testStorageProvider) DeleteObjectsWithPrefix(context.Context, string) error {
	return nil
}

func (p *testStorageProvider) UploadSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (p *testStorageProvider) OpenBlob(ctx context.Context, _ string, _ storage.ObjectType) (storage.Blob, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}

	if p.openBlobErr != nil {
		return nil, p.openBlobErr
	}

	return testBlob{}, nil
}

func (p *testStorageProvider) OpenSeekable(context.Context, string, storage.SeekableObjectType) (storage.Seekable, error) {
	return nil, errors.New("unexpected seekable open")
}

func (p *testStorageProvider) GetDetails() string {
	return "test storage provider"
}

type testBlob struct{}

func (testBlob) WriteTo(context.Context, io.Writer) (int64, error) {
	return 0, nil
}

func (testBlob) Put(context.Context, []byte) error {
	return nil
}

func (testBlob) Exists(context.Context) (bool, error) {
	return true, nil
}

func testHeader(t *testing.T, buildID string) *header.Header {
	t.Helper()

	id := uuid.MustParse(buildID)
	h, err := header.NewHeader(header.NewTemplateMetadata(id, 4096, 4096), nil)
	require.NoError(t, err)

	return h
}

func newStorageTemplateForTest(
	t *testing.T,
	c *Cache,
	buildID string,
	provider storage.StorageProvider,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
) *storageTemplate {
	t.Helper()

	tmpl, err := newTemplateFromStorage(
		c.config.BuilderConfig,
		buildID,
		memfileHeader,
		rootfsHeader,
		provider,
		blockmetrics.Metrics{},
		testTemplateFile{path: "snapfile"},
		testTemplateFile{path: "metadata"},
	)
	require.NoError(t, err)

	return tmpl
}

// simulateGetTemplate mimics getTemplateWithFetch's lock-protected TTL logic
// without needing a full storageTemplate (which requires disk paths).
func simulateGetTemplate(c *Cache, key string, maxSandboxLengthHours int64) {
	ttl := templateExpiration
	if maxSandboxLengthHours > 0 {
		ttl = max(ttl, time.Duration(maxSandboxLengthHours)*time.Hour+templateExpirationBuffer)
	}

	c.extendMu.Lock()
	t, found := c.cache.GetOrSet(key, nil, ttlcache.WithTTL[string, Template](ttl))
	if found && t.TTL() < ttl {
		c.cache.Set(key, t.Value(), ttl)
	}
	c.extendMu.Unlock()
}

func TestGetTemplate_ExtendsTTL(t *testing.T) {
	t.Parallel()

	defaultTTL := 50 * time.Millisecond
	c := newTestCache(defaultTTL)
	go c.cache.Start()
	defer c.cache.Stop()

	key := "build-long-running"
	c.cache.Set(key, nil, defaultTTL)

	simulateGetTemplate(c, key, 168)

	item := c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, 168*time.Hour+templateExpirationBuffer, item.TTL())

	time.Sleep(defaultTTL + 20*time.Millisecond)

	item = c.cache.Get(key)
	assert.NotNil(t, item, "entry must survive past the original default TTL")
}

func TestGetTemplate_NeverShortens(t *testing.T) {
	t.Parallel()

	c := newTestCache(time.Hour)
	key := "build-shared"

	simulateGetTemplate(c, key, 168)

	item := c.cache.Get(key)
	require.NotNil(t, item)
	longTTL := item.TTL()

	simulateGetTemplate(c, key, 24)

	item = c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, longTTL, item.TTL(), "TTL must not decrease when a shorter team accesses the template")
}

func TestGetTemplate_DefaultTTLForZero(t *testing.T) {
	t.Parallel()

	c := newTestCache(time.Hour)
	key := "build-default"

	simulateGetTemplate(c, key, 0)

	item := c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, templateExpiration, item.TTL())
}

func TestGetTemplate_SetDoesNotTriggerOnEviction(t *testing.T) {
	t.Parallel()

	inner := ttlcache.New(ttlcache.WithTTL[string, Template](time.Hour))

	evicted := false
	inner.OnEviction(func(_ context.Context, _ ttlcache.EvictionReason, _ *ttlcache.Item[string, Template]) {
		evicted = true
	})

	c := &Cache{cache: inner}

	key := "build-1"
	c.cache.Set(key, nil, ttlcache.DefaultTTL)
	simulateGetTemplate(c, key, 168)

	assert.False(t, evicted, "Set() on existing key must NOT trigger OnEviction")

	item := c.cache.Get(key)
	require.NotNil(t, item)
	assert.Equal(t, 168*time.Hour+templateExpirationBuffer, item.TTL())
}

func TestWithoutExtend_EntryEvictedEarly(t *testing.T) {
	t.Parallel()

	defaultTTL := 50 * time.Millisecond
	c := newTestCache(defaultTTL)
	go c.cache.Start()
	defer c.cache.Stop()

	key := "build-will-expire"
	c.cache.Set(key, nil, defaultTTL)

	time.Sleep(defaultTTL + 30*time.Millisecond)

	item := c.cache.Get(key)
	assert.Nil(t, item, "without TTL extension, the entry should be evicted after the default TTL")
}

func TestGetTemplateWithFetch_RetriesAfterFetchFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := newFetchTestCache(t)
	buildID := uuid.NewString()
	memfileHeader := testHeader(t, buildID)

	failedTemplate := newStorageTemplateForTest(
		t,
		c,
		buildID,
		&testStorageProvider{openBlobErr: errors.New("gcs unavailable")},
		memfileHeader,
		nil,
	)
	got := c.getTemplateWithFetch(ctx, failedTemplate, 0)
	require.Same(t, failedTemplate, got)

	_, err := got.Rootfs()
	require.ErrorContains(t, err, "gcs unavailable")
	require.Eventually(t, func() bool {
		_, found := c.GetCachedTemplate(buildID)

		return !found
	}, time.Second, time.Millisecond)

	successfulTemplate := newStorageTemplateForTest(
		t,
		c,
		buildID,
		&testStorageProvider{},
		memfileHeader,
		testHeader(t, buildID),
	)
	got = c.getTemplateWithFetch(ctx, successfulTemplate, 0)
	require.Same(t, successfulTemplate, got)

	rootfs, err := got.Rootfs()
	require.NoError(t, err)
	require.NotNil(t, rootfs)
	require.Eventually(t, func() bool {
		cached, ok := c.GetCachedTemplate(buildID)

		return ok && cached == got
	}, time.Second, time.Millisecond)
}

func TestGetTemplateWithFetch_SharesCachedFetchBeforeEviction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := newFetchTestCache(t)
	buildID := uuid.NewString()
	memfileHeader := testHeader(t, buildID)
	release := make(chan struct{})

	firstTemplate := newStorageTemplateForTest(
		t,
		c,
		buildID,
		&testStorageProvider{openBlobErr: errors.New("gcs unavailable"), release: release},
		memfileHeader,
		nil,
	)
	got := c.getTemplateWithFetch(ctx, firstTemplate, 0)
	require.Same(t, firstTemplate, got)

	secondTemplate := newStorageTemplateForTest(
		t,
		c,
		buildID,
		&testStorageProvider{},
		memfileHeader,
		testHeader(t, buildID),
	)
	got = c.getTemplateWithFetch(ctx, secondTemplate, 0)
	require.Same(t, firstTemplate, got)

	close(release)
	_, err := got.Rootfs()
	require.ErrorContains(t, err, "gcs unavailable")
}
