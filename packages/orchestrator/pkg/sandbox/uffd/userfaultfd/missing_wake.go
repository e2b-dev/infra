//go:build linux

package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

// faultPageViaMemfdWake installs a MISSING fault by writing source bytes
// into the FC-shared memfd and calling UFFDIO_WAKE, skipping the
// UFFDIO_COPY kernel memcpy. Read faults arm WP first.
func (u *Userfaultfd) faultPageViaMemfdWake(
	ctx context.Context,
	addr uintptr,
	offset int64,
	accessType block.AccessType,
	source PageReader,
	memfd *block.Memfd,
	onFailure func() error,
) (outcome faultOutcome, err error) {
	span := trace.SpanFromContext(ctx)
	pageSize := int64(u.pageSize)
	offset &^= pageSize - 1

	defer func() {
		if r := recover(); r != nil {
			u.logger.Error(ctx, "UFFD memfd-wake panic", zap.Any("panic", r))
			outcome = faultDiscarded
			err = fmt.Errorf("uffd memfd-wake panic: %v", r)
		}
	}()

	if accessType == block.Read {
		if err := u.fd.writeProtect(addr, u.pageSize, UFFDIO_WRITEPROTECT_MODE_WP); err != nil {
			if errors.Is(err, unix.ESRCH) {
				span.SetAttributes(attribute.Bool("uffd.process_exited", true))

				return faultDiscarded, nil
			}
			if errors.Is(err, unix.EAGAIN) {
				return faultDeferred, nil
			}

			joined := errors.Join(err, safeInvoke(onFailure))
			span.RecordError(joined)
			u.logger.Error(ctx, "UFFD memfd-wake writeProtect error", zap.Error(joined))

			return faultDiscarded, fmt.Errorf("pre-WAKE writeProtect: %w", joined)
		}
	}

	dst := memfd.Bytes()
	if int64(len(dst)) < offset+pageSize {
		err := fmt.Errorf("memfd too small: offset=%d page=%d size=%d", offset, pageSize, len(dst))
		span.RecordError(err)

		return faultDiscarded, errors.Join(err, safeInvoke(onFailure))
	}

	page := dst[offset : offset+pageSize]

	var dataErr error
	var attempt int

retryLoop:
	for attempt = range sliceMaxRetries + 1 {
		var n int
		n, dataErr = source.ReadAt(ctx, page, offset)
		if dataErr == nil && int64(n) != pageSize {
			dataErr = fmt.Errorf("short read at %d: got %d, want %d", offset, n, pageSize)
		}
		if dataErr == nil {
			break
		}
		if attempt >= sliceMaxRetries || ctx.Err() != nil {
			break
		}

		u.logger.Warn(ctx, "UFFD memfd-wake read error, retrying",
			zap.Int("attempt", attempt+1),
			zap.Error(dataErr),
		)

		delay := min(sliceRetryBaseDelay<<attempt, sliceRetryMaxDelay)
		jitter := time.Duration(rand.Int63n(int64(delay) / 2))
		backoff := time.NewTimer(delay + jitter)
		select {
		case <-ctx.Done():
			backoff.Stop()
			dataErr = errors.Join(dataErr, ctx.Err())

			break retryLoop
		case <-backoff.C:
		}
	}

	if dataErr != nil {
		joined := errors.Join(dataErr, safeInvoke(onFailure))
		span.RecordError(joined)
		u.logger.Error(ctx, "UFFD memfd-wake read error after retries",
			zap.Int("attempts", attempt+1), zap.Error(joined))

		return faultDiscarded, fmt.Errorf("read source after %d attempts: %w", attempt+1, joined)
	}

	if err := u.fd.wake(addr, u.pageSize); err != nil {
		if errors.Is(err, unix.ESRCH) {
			span.SetAttributes(attribute.Bool("uffd.process_exited", true))

			return faultDiscarded, nil
		}
		if errors.Is(err, unix.EAGAIN) {
			return faultDeferred, nil
		}

		joined := errors.Join(err, safeInvoke(onFailure))
		span.RecordError(joined)
		u.logger.Error(ctx, "UFFD memfd-wake UFFDIO_WAKE error", zap.Error(joined))

		return faultDiscarded, fmt.Errorf("UFFDIO_WAKE: %w", joined)
	}

	return faultInstalled, nil
}
