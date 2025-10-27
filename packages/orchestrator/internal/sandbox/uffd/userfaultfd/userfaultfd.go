package userfaultfd

import (
	"errors"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type Userfaultfd struct {
	fd uintptr

	src block.Slicer
	ma  *memory.Mapping

	missingRequests map[int64]struct{}

	wg errgroup.Group

	logger *zap.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, m *memory.Mapping, logger *zap.Logger) (*Userfaultfd, error) {
	return &Userfaultfd{
		fd:              fd,
		src:             src,
		missingRequests: make(map[int64]struct{}),
		ma:              m,
		logger:          logger,
	}, nil
}
