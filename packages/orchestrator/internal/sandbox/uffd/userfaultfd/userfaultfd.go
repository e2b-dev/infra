package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

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

	// The maps prevent serving pages multiple times (as we now add WP only once we don't have to remove entries from any map.)
	// For normal sized pages with swap on, the behavior seems not to be properly described in docs
	// and it's not clear if the missing can be legitimately triggered multiple times.
	missingRequests *block.Tracker
	writeRequests   *block.Tracker
	wpRequests      *block.Tracker

	writeRequestCounter utils.WaitCounter
	wg                  errgroup.Group

	logger *zap.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, pagesize int64, m *memory.Mapping, logger *zap.Logger) (*Userfaultfd, error) {
	return &Userfaultfd{
		fd:                  fd,
		src:                 src,
		dirty:               block.NewTracker(pagesize),
		missingRequests:     block.NewTracker(pagesize),
		writeRequests:       block.NewTracker(pagesize),
		wpRequests:          block.NewTracker(pagesize),
		disabled:            atomic.Bool{},
		ma:                  m,
		writeRequestCounter: utils.WaitCounter{},
		logger:              logger,
	}, nil
}

func (u *Userfaultfd) Disable() {
	u.disabled.Store(true)
}

func (u *Userfaultfd) Dirty(ctx context.Context) (*block.Tracker, error) {
	err := u.writeRequestCounter.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for write requests: %w", err)
	}

	u.missingRequests.Reset()
	u.writeRequests.Reset()
	u.wpRequests.Reset()

	return u.dirty.Clone(), nil
}
