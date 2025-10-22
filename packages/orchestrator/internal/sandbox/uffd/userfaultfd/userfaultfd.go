package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type Userfaultfd struct {
	fd uintptr

	src      block.Slicer
	ma       *memory.Mapping
	dirty    *block.Tracker
	disabled atomic.Bool

	missingRequests *block.Tracker
	workerSem       *semaphore.Weighted

	writesInProgress *utils.SettleCounter
	wg               errgroup.Group

	logger *zap.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, pagesize int64, m *memory.Mapping, logger *zap.Logger) (*Userfaultfd, error) {
	return &Userfaultfd{
		fd:               fd,
		src:              src,
		dirty:            block.NewTracker(pagesize),
		missingRequests:  block.NewTracker(pagesize),
		disabled:         atomic.Bool{},
		workerSem:        semaphore.NewWeighted(2048),
		ma:               m,
		writesInProgress: utils.NewZeroSettleCounter(),
		logger:           logger,
	}, nil
}

func (u *Userfaultfd) Disable() {
	u.disabled.Store(true)
}

func (u *Userfaultfd) Dirty(ctx context.Context) (*block.Tracker, error) {
	err := u.writesInProgress.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for write requests: %w", err)
	}

	u.missingRequests.Reset()

	return u.dirty.Clone(), nil
}
