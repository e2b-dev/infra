package userfaultfd

import (
	"errors"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

const (
	workerSemaphoreWeight = 2048
)

type Userfaultfd struct {
	fd uintptr

	src block.Slicer
	ma  *memory.Mapping

	missingRequests *block.Tracker
	workerSem       *semaphore.Weighted

	wg errgroup.Group

	logger *zap.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, pagesize int64, m *memory.Mapping, logger *zap.Logger) (*Userfaultfd, error) {
	return &Userfaultfd{
		fd:              fd,
		src:             src,
		missingRequests: block.NewTracker(pagesize),
		workerSem:       semaphore.NewWeighted(workerSemaphoreWeight),
		ma:              m,
		logger:          logger,
	}, nil
}
