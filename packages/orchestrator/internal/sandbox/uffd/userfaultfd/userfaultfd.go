package userfaultfd

import (
	"context"
	"errors"
	"fmt"

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

	missingRequests *block.Tracker

	wg errgroup.Group

	logger *zap.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, m *memory.Mapping, pagesize int64, logger *zap.Logger) (*Userfaultfd, error) {
	return &Userfaultfd{
		fd:              fd,
		src:             src,
		missingRequests: block.NewTracker(pagesize),
		ma:              m,
		logger:          logger,
	}, nil
}

func (u *Userfaultfd) Unregister() error {
	for _, r := range u.ma.Regions {
		if err := u.unregister(r.BaseHostVirtAddr, uint64(r.Size)); err != nil {
			return fmt.Errorf("failed to unregister: %w", err)
		}
	}

	return nil
}

func (u *Userfaultfd) Dirty(ctx context.Context) (*block.Tracker, error) {
	return u.missingRequests.Clone(), nil
}
