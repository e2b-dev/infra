package synchronization

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

type testStore struct {
	mu sync.Mutex

	source []string
	pool   map[string]string

	inserts int
	removes int
}

func newTestStore(source []string, preExistingPool []string) *testStore {
	pool := make(map[string]string, len(preExistingPool))
	for _, k := range preExistingPool {
		pool[k] = k
	}

	return &testStore{source: source, pool: pool}
}

func (s *testStore) SourceList(ctx context.Context) ([]string, error) {
	return append([]string(nil), s.source...), nil
}

func (s *testStore) SourceExists(ctx context.Context, source []string, p string) bool {
	for _, v := range source {
		if v == p {
			return true
		}
	}

	return false
}

func (s *testStore) PoolList(ctx context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0)
	for k := range s.pool {
		out = append(out, k)
	}

	return out
}

func (s *testStore) PoolExists(ctx context.Context, item string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pool[item]
	return ok
}

func (s *testStore) PoolInsert(ctx context.Context, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool[value] = value
	s.inserts++
}

func (s *testStore) PoolUpdate(ctx context.Context, value string) { /* not used */ }

func (s *testStore) PoolRemove(ctx context.Context, item string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range s.pool {
		if v == item {
			delete(s.pool, k)
			break
		}
	}

	s.removes++
}

func newSynchronizer(store Store[string, string]) *Synchronize[string, string] {
	zap.ReplaceGlobals(zap.NewNop())
	return &Synchronize[string, string]{
		store:            store,
		tracer:           noop.NewTracerProvider().Tracer("test"),
		tracerSpanPrefix: "test synchronization",
		logsPrefix:       "test synchronization",
	}
}

func TestSynchronize_InsertAndRemove(t *testing.T) {
	ctx := context.Background()

	// Start with empty pool; source has a & b.
	s := newTestStore([]string{"a", "b"}, nil)
	syncer := newSynchronizer(s)

	if err := syncer.sync(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want, got := 2, s.inserts; want != got {
		t.Fatalf("insert count mismatch: want %d got %d", want, got)
	}

	if len(s.pool) != 2 {
		t.Fatalf("pool size want 2 got %d", len(s.pool))
	}

	// Now remove "b" from the source – should trigger exactly one removal.
	s.source = []string{"a"}
	if err := syncer.sync(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want, got := 1, s.removes; want != got {
		t.Fatalf("remove count mismatch: want %d got %d", want, got)
	}

	if len(s.pool) != 1 || !s.PoolExists(ctx, "a") {
		t.Fatalf("pool contents after removal are incorrect: %#v", s.pool)
	}
}
