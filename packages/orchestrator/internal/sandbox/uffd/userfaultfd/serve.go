package userfaultfd

/*
This is the flow of the UFFD events:

```mermaid
flowchart TD
A[missing page] -- write (WRITE flag) --> B(COPY) --> C[mark as dirty]
A -- read (0 flag) --> D(COPY + WP protect) --> E[faulted page]
E -- write (WP|WRITE flag) --> F(remove WP) --> C
```
*/

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
)

func (u *userfaultfd) Serve(
	mappings mapping.Mappings,
	src block.Slicer,
	fdExit *fdexit.FdExit,
	logger *zap.Logger,
) error {
	defer fmt.Fprintf(os.Stderr, "exiting serve >>>>>>>>>>>>")
	pollFds := []unix.PollFd{
		{Fd: int32(u.fd), Events: unix.POLLIN},
		{Fd: int32(fdExit.Reader()), Events: unix.POLLIN},
	}

	var eg errgroup.Group

	missingChunksBeingHandled := map[int64]struct{}{}

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

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			n, err := syscall.Read(int(u.fd), buf)
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

				// Continue polling the fd.
				continue outerLoop
			}

			logger.Error("uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*UffdMsg)(unsafe.Pointer(&buf[0]))
		if GetMsgEvent(&msg) != UFFD_EVENT_PAGEFAULT {
			logger.Error("UFFD serve unexpected event type", zap.Any("event_type", GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := GetMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := GetPagefaultAddress(&pagefault)

		offset, pagesize, err := mappings.GetRange(addr)
		if err != nil {
			logger.Error("UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		if pagefault.flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
			eg.Go(func() error {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("UFFD serve panic", zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))
					}
				}()

				wpErr := u.removeWriteProtection(addr, pagesize)
				if wpErr != nil {
					return fmt.Errorf("error removing write protection from page %d", addr)
				}

				return nil
			})

			continue
		}

		// This prevents serving missing pages multiple times.
		// For normal sized pages with swap on, the behavior seems not to be properly described in docs
		// and it's not clear if the missing can be legitimately triggered multiple times.
		if _, ok := missingChunksBeingHandled[offset]; ok {
			continue
		}

		missingChunksBeingHandled[offset] = struct{}{}

		if pagefault.flags == 0 {
			// fmt.Fprintf(os.Stderr, "read trigger %d %d\n", addr, offset/pagesize)
		}

		if pagefault.flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			// fmt.Fprintf(os.Stderr, "write trigger %d %d\n", addr, offset/pagesize)
		}

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("UFFD serve panic", zap.Any("pagesize", pagesize), zap.Any("panic", r))
				}
			}()

			var copyMode CULong

			b, sliceErr := src.Slice(offset, int64(pagesize))
			if sliceErr != nil {
				signalErr := fdExit.SignalExit()

				joinedErr := errors.Join(sliceErr, signalErr)

				logger.Error("UFFD serve slice error", zap.Error(joinedErr))

				return fmt.Errorf("failed to read from source: %w", joinedErr)
			}

			copyErr := u.copy(addr, b, pagesize, copyMode)
			if copyErr == unix.EEXIST {
				logger.Debug("UFFD serve page already mapped", zap.Any("offset", addr), zap.Any("pagesize", pagesize))

				// Page is already mapped

				return nil
			}

			if copyErr != nil {
				signalErr := fdExit.SignalExit()

				joinedErr := errors.Join(copyErr, signalErr)

				logger.Error("UFFD serve uffdio copy error", zap.Error(joinedErr))

				return fmt.Errorf("failed uffdio copy %w", joinedErr)
			}

			return nil
		})
	}
}
