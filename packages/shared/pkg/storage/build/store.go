package build

import (
	"fmt"
	"io"
	"sync"
)

type Store struct {
	sources       map[string]io.ReaderAt
	mu            sync.Mutex
	sourceFactory func(id string) (io.ReaderAt, error)
}

func NewStore(
	sourceFactory func(id string) (io.ReaderAt, error),
) *Store {
	return &Store{
		sources:       make(map[string]io.ReaderAt),
		sourceFactory: sourceFactory,
	}
}

func (s *Store) Get(id string) (io.ReaderAt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	source, ok := s.sources[id]
	if !ok {
		source, err := s.sourceFactory(id)
		if err != nil {
			return nil, fmt.Errorf("failed to create source: %w", err)
		}

		s.sources[id] = source
	}

	return source, nil
}
