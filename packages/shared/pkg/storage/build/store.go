package build

import (
	"context"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type Store struct {
	bucket  *gcs.BucketHandle
	sources map[string]*gcs.Object
	mu      sync.Mutex
	ctx     context.Context
}

func NewStore(bucket *gcs.BucketHandle, ctx context.Context) *Store {
	return &Store{
		bucket:  bucket,
		sources: make(map[string]*gcs.Object),
		ctx:     ctx,
	}
}

func (s *Store) Get(id string) *gcs.Object {
	s.mu.Lock()
	defer s.mu.Unlock()

	source, ok := s.sources[id]
	if !ok {
		source = gcs.NewObject(s.ctx, s.bucket, id)
	}

	s.sources[id] = source

	return source
}
