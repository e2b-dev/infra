package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type Userfaultfd struct {
	fd uffdFd

	src block.Slicer
	ma  *memory.Mapping

	missingRequests sync.Map

	wg errgroup.Group

	logger logger.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, m *memory.Mapping, logger logger.Logger) (*Userfaultfd, error) {
	return &Userfaultfd{
		fd:              uffdFd(fd),
		src:             src,
		missingRequests: sync.Map{},
		ma:              m,
		logger:          logger,
	}, nil
}

func (u *Userfaultfd) Close() error {
	return u.fd.close()
}

func (u *Userfaultfd) Serve(
	ctx context.Context,
	fdExit *fdexit.FdExit,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(u.fd), Events: unix.POLLIN},
		{Fd: fdExit.Reader(), Events: unix.POLLIN},
	}

	eagainCounter := newEagainCounter(u.logger, "uffd: eagain during fd read (accumulated)")
	defer eagainCounter.Close()

outerLoop:
	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				u.logger.Debug(ctx, "uffd: interrupted polling, going back to polling")

				continue
			}

			if err == unix.EAGAIN {
				u.logger.Debug(ctx, "uffd: eagain during polling, going back to polling")

				continue
			}

			u.logger.Error(ctx, "UFFD serve polling error", zap.Error(err))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := u.wg.Wait()
			if errMsg != nil {
				u.logger.Warn(ctx, "UFFD fd exit error while waiting for goroutines to finish", zap.Error(errMsg))

				return fmt.Errorf("failed to handle uffd: %w", errMsg)
			}

			return nil
		}

		uffdFd := pollFds[0]
		if uffdFd.Revents&unix.POLLIN == 0 {
			// Uffd is not ready for reading as there is nothing to read on the fd.
			// https://github.com/firecracker-microvm/firecracker/issues/5056
			// https://elixir.bootlin.com/linux/v6.8.12/source/fs/userfaultfd.c#L1149
			// TODO: Check for all the errors
			// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html
			// - https://elixir.bootlin.com/linux/v6.8.12/source/fs/userfaultfd.c
			// - https://man7.org/linux/man-pages/man2/userfaultfd.2.html
			// It might be possible to just check for data != 0 in the syscall.Read loop
			// but I don't feel confident about doing that.
			u.logger.Debug(ctx, "uffd: no data in fd, going back to polling")

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			_, err := syscall.Read(int(u.fd), buf)
			if err == syscall.EINTR {
				u.logger.Debug(ctx, "uffd: interrupted read, reading again")

				continue
			}

			if err == nil {
				// There is no error so we can proceed.

				eagainCounter.Log()

				break
			}

			if err == syscall.EAGAIN {
				eagainCounter.Increase()

				// Continue polling the fd.
				continue outerLoop
			}

			u.logger.Error(ctx, "uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*UffdMsg)(unsafe.Pointer(&buf[0]))

		if msgEvent := getMsgEvent(&msg); msgEvent != UFFD_EVENT_PAGEFAULT {
			u.logger.Error(ctx, "UFFD serve unexpected event type", zap.Any("event_type", msgEvent))

			return ErrUnexpectedEventType
		}

		arg := getMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))
		flags := pagefault.flags

		addr := getPagefaultAddress(&pagefault)

		offset, pagesize, err := u.ma.GetOffset(addr)
		if err != nil {
			u.logger.Error(ctx, "UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		// Handle write to missing page (WRITE flag)
		// If the event has WRITE flag, it was a write to a missing page.
		// For the write to be executed, we first need to copy the page from the source to the guest memory.
		if flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			err := u.handleMissing(ctx, fdExit.SignalExit, addr, offset, pagesize)
			if err != nil {
				return fmt.Errorf("failed to handle missing write: %w", err)
			}

			continue
		}

		// Handle read to missing page ("MISSING" flag)
		// If the event has no flags, it was a read to a missing page and we need to copy the page from the source to the guest memory.
		if flags == 0 {
			err := u.handleMissing(ctx, fdExit.SignalExit, addr, offset, pagesize)
			if err != nil {
				return fmt.Errorf("failed to handle missing: %w", err)
			}

			continue
		}

		// MINOR and WP flags are not expected as we don't register the uffd with these flags.
		return fmt.Errorf("unexpected event type: %d, closing uffd", flags)
	}
}

func (u *Userfaultfd) handleMissing(
	ctx context.Context,
	onFailure func() error,
	addr uintptr,
	offset int64,
	pagesize uint64,
) error {
	if _, ok := u.missingRequests.Load(offset); ok {
		return nil
	}

	u.missingRequests.Store(offset, struct{}{})

	u.wg.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", pagesize), zap.Any("panic", r))
			}
		}()

		b, sliceErr := u.src.Slice(ctx, offset, int64(pagesize))
		if sliceErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(sliceErr, signalErr)

			u.logger.Error(ctx, "UFFD serve slice error", zap.Error(joinedErr))

			return fmt.Errorf("failed to read from source: %w", joinedErr)
		}

		var copyMode CULong

		copyErr := u.fd.copy(addr, b, pagesize, copyMode)
		if errors.Is(copyErr, unix.EEXIST) {
			// Page is already mapped

			return nil
		}

		if copyErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(copyErr, signalErr)

			u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

			return fmt.Errorf("failed uffdio copy %w", joinedErr)
		}

		return nil
	})

	return nil
}
