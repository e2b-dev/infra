package uffd

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

var tracer = otel.Tracer("orchestrator.internal.sandbox.uffd")

type GuestRegionUffdMapping struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	PageSize         uintptr `json:"page_size_kib"`
}

func Serve(
	ctx context.Context,
	uffd int,
	mappings mapping.Mappings,
	src block.Slicer,
	fdExit *fdexit.FdExit,
	logger *zap.Logger,
) error {
	ctx, span := tracer.Start(ctx, "uffd-serve", trace.WithAttributes(
		attribute.Int("uffd_handle", uffd),
	))
	defer span.End()

	pollFds := []unix.PollFd{
		{Fd: int32(uffd), Events: unix.POLLIN},
		{Fd: fdExit.Reader(), Events: unix.POLLIN},
	}

	var eg errgroup.Group

	missingPagesBeingHandled := map[int64]struct{}{}

outerLoop:
	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				logger.Debug("uffd: interrupted polling, going back to polling")

				continue
			}

			if err == unix.EAGAIN {
				logger.Debug("uffd: eagain during polling, going back to polling")
				time.Sleep(1 * time.Millisecond)

				continue
			}

			logger.Error("UFFD serve polling error", zap.Error(err))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				logger.Warn("UFFD fd exit error while waiting for goroutines to finish", zap.Error(errMsg))

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
			logger.Debug("uffd: no data in fd, going back to polling")

			continue
		}

		buf := make([]byte, unsafe.Sizeof(userfaultfd.UffdMsg{}))

		for {
			n, err := syscall.Read(uffd, buf)
			if err == syscall.EINTR {
				logger.Debug("uffd: interrupted read, reading again")

				continue
			}

			if err == nil {
				// There is no error so we can proceed.
				break
			}

			if err == syscall.EAGAIN {
				logger.Debug("uffd: eagain error, going back to polling", zap.Error(err), zap.Int("read_bytes", n))
				time.Sleep(1 * time.Millisecond)

				// Continue polling the fd.
				continue outerLoop
			}

			logger.Error("uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*userfaultfd.UffdMsg)(unsafe.Pointer(&buf[0]))
		if userfaultfd.GetMsgEvent(&msg) != userfaultfd.UFFD_EVENT_PAGEFAULT {
			logger.Error("UFFD serve unexpected event type", zap.Any("event_type", userfaultfd.GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := userfaultfd.GetMsgArg(&msg)
		pagefault := *(*userfaultfd.UffdPagefault)(unsafe.Pointer(&arg[0]))

		addr := userfaultfd.GetPagefaultAddress(&pagefault)

		offset, pageSize, err := mappings.GetRange(uintptr(addr))
		if err != nil {
			logger.Error("UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		if _, ok := missingPagesBeingHandled[offset]; ok {
			continue
		}

		missingPagesBeingHandled[offset] = struct{}{}

		eg.Go(fetchSlab(ctx, offset, pageSize, logger, src, fdExit, uffd, addr))
	}
}

func fetchSlab(
	ctx context.Context,
	offset, pageSize int64,
	logger *zap.Logger,
	src block.Slicer,
	fdExit *fdexit.FdExit,
	uffd int,
	addr userfaultfd.CULong,
) func() error {
	return func() error {
		ctx, span := tracer.Start(ctx, "uffd-serve-slab", trace.WithAttributes(
			attribute.Int64("offset", offset),
			attribute.Int64("page-size", pageSize),
		))
		defer span.End()

		defer func() {
			if r := recover(); r != nil {
				logger.Error("UFFD serve panic",
					zap.Any("offset", offset),
					zap.Any("pagesize", pageSize),
					zap.Any("panic", r))
			}
		}()

		data, err := read(ctx, offset, pageSize, src)
		if err != nil {
			signalErr := fdExit.SignalExit()

			joinedErr := errors.Join(err, signalErr)

			logger.Error("UFFD serve slice error", zap.Error(joinedErr))

			return fmt.Errorf("failed to read from source: %w", joinedErr)
		}

		if err = copy(ctx, data, offset, pageSize, uffd, addr, logger); err != nil {
			signalErr := fdExit.SignalExit()

			joinedErr := errors.Join(err, signalErr)

			logger.Error("UFFD serve uffdio copy error", zap.Error(joinedErr))

			return fmt.Errorf("failed uffdio copy %w", joinedErr)

		}

		return nil
	}
}

func read(ctx context.Context, offset, pageSize int64, src block.Slicer) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "uffd-serve-read", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int64("page-size", pageSize),
	))
	defer span.End()

	b, err := src.Slice(ctx, offset, pageSize)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func copy(ctx context.Context, b []byte, offset, pageSize int64, uffd int, addr userfaultfd.CULong, logger *zap.Logger) error {
	ctx, span := tracer.Start(ctx, "uffd-serve-copy", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int64("page-size", pageSize),
	))
	defer span.End()

	cpy := userfaultfd.NewUffdioCopy(
		b,
		addr&^userfaultfd.CULong(pageSize-1),
		userfaultfd.CULong(pageSize),
		0,
		0,
	)

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(uffd),
		userfaultfd.UFFDIO_COPY,
		uintptr(unsafe.Pointer(&cpy)),
	); err != 0 {
		if err == unix.EEXIST {
			logger.Debug("UFFD serve page already mapped",
				zap.Any("offset", offset),
				zap.Any("pagesize", pageSize))

			// Page is already mapped
			return nil
		}

		return err
	}

	return nil
}
