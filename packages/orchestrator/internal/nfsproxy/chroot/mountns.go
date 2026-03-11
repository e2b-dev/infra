package chroot

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type NSPathNotExistError struct{ msg string }

func (e NSPathNotExistError) Error() string { return e.msg }

const (
	// https://github.com/torvalds/linux/blob/master/include/uapi/linux/magic.h
	NSFS_MAGIC   = unix.NSFS_MAGIC
	PROCFS_MAGIC = unix.PROC_SUPER_MAGIC
)

type mountNS struct {
	file   *os.File
	closed bool

	mu     sync.Mutex
	reqCh  chan nsRequest
	stopCh chan struct{}
	doneCh chan struct{}
}

var ErrNamespaceClosed = fmt.Errorf("namespace is closed")

func (ns *mountNS) errorIfClosed() error {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	if ns.closed {
		return fmt.Errorf("%w: %q has already been closed", ErrNamespaceClosed, ns.file.Name())
	}

	return nil
}

func (ns *mountNS) Set() error {
	if err := ns.errorIfClosed(); err != nil {
		return err
	}

	if err := unix.Setns(int(ns.Fd()), unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("Error switching to ns %v: %w", ns.file.Name(), err)
	}

	return nil
}

func (ns *mountNS) Close() error {
	ns.mu.Lock()
	if ns.closed {
		ns.mu.Unlock()

		return fmt.Errorf("%w: %q has already been closed", ErrNamespaceClosed, ns.file.Name())
	}
	ns.closed = true
	stopCh := ns.stopCh
	doneCh := ns.doneCh
	file := ns.file
	ns.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
		<-doneCh
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close %q: %w", file.Name(), err)
	}

	return nil
}

func (ns *mountNS) Path() string {
	return ns.file.Name()
}

func (ns *mountNS) Fd() uintptr {
	return ns.file.Fd()
}

func (ns *mountNS) Do(toRun func() error) error {
	if err := ns.errorIfClosed(); err != nil {
		return err
	}

	ns.mu.Lock()
	reqCh := ns.reqCh
	stopCh := ns.stopCh
	ns.mu.Unlock()

	if reqCh != nil {
		done := make(chan error, 1)

		select {
		case reqCh <- nsRequest{fn: toRun, done: done}:
			return <-done
		case <-stopCh:
			return fmt.Errorf("mount namespace %q is closed", ns.file.Name())
		}
	}

	containedCall := func() error {
		threadNS, err := getCurrentNSNoLock()
		if err != nil {
			return fmt.Errorf("failed to open current mountns: %w", err)
		}
		defer func() { _ = threadNS.Close() }()

		if err = ns.Set(); err != nil {
			return fmt.Errorf("error switching to ns %v: %w", ns.file.Name(), err)
		}
		defer func() {
			if err := threadNS.Set(); err == nil {
				runtime.UnlockOSThread()
			}
		}()

		return toRun()
	}

	var wg sync.WaitGroup
	var innerError error

	wg.Go(func() {
		runtime.LockOSThread()
		innerError = containedCall()
	})
	wg.Wait()

	return innerError
}

type NSPathNotNSError struct{ msg string }

func (e NSPathNotNSError) Error() string { return e.msg }

func IsNSorErr(nspath string) error {
	stat := unix.Statfs_t{}
	if err := unix.Statfs(nspath, &stat); err != nil {
		if os.IsNotExist(err) {
			err = NSPathNotExistError{msg: fmt.Sprintf("failed to Statfs %q: %v", nspath, err)}
		} else {
			err = fmt.Errorf("failed to Statfs %q: %w", nspath, err)
		}

		return err
	}

	switch stat.Type {
	case PROCFS_MAGIC, NSFS_MAGIC:
		return nil
	default:
		return NSPathNotNSError{msg: fmt.Sprintf("unknown FS magic on %q: %x", nspath, stat.Type)}
	}
}

func getNS(nspath string) (*mountNS, error) {
	err := IsNSorErr(nspath)
	if err != nil {
		return nil, err
	}

	fd, err := os.Open(nspath)
	if err != nil {
		return nil, err
	}

	return &mountNS{file: fd}, nil
}

func getCurrentNSNoLock() (*mountNS, error) {
	return getNS(getCurrentThreadMountNSPath())
}

func getCurrentThreadMountNSPath() string {
	return fmt.Sprintf("/proc/%d/task/%d/ns/mnt", os.Getpid(), unix.Gettid())
}

func tempMountNS(ctx context.Context) (*mountNS, error) {
	type result struct {
		ns  *mountNS
		err error
	}

	resultCh := make(chan result, 1)

	go func() {
		// take ownership of this os thread so we don't have to keep swapping mount namespaces
		runtime.LockOSThread()

		// get the original mount namespace
		threadNS, err := getCurrentNSNoLock()
		if err != nil {
			resultCh <- result{err: fmt.Errorf("failed to open current namespace: %w", err)}
			runtime.UnlockOSThread()

			return
		}

		// close the thread when we're done
		defer func() {
			if err := threadNS.Close(); err != nil {
				logger.L().Error(ctx, "failed to close current namespace", zap.Error(err))
			}
		}()

		// create a new mount namespace
		if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
			resultCh <- result{err: fmt.Errorf("failed to unshare namespace: %w", err)}
			runtime.UnlockOSThread()

			return
		}

		// get the mount namespace that we just created
		tempNS, err := getCurrentNSNoLock()
		if err != nil {
			_ = threadNS.Set()
			resultCh <- result{err: fmt.Errorf("failed to open temporary mount namespace: %w", err)}
			runtime.UnlockOSThread()

			return
		}

		tempNS.reqCh = make(chan nsRequest)
		tempNS.stopCh = make(chan struct{})
		tempNS.doneCh = make(chan struct{})

		resultCh <- result{ns: tempNS}

		for {
			select {
			case req := <-tempNS.reqCh:
				req.done <- req.fn()
			case <-tempNS.stopCh:
				_ = threadNS.Set()
				close(tempNS.doneCh)
				runtime.UnlockOSThread()

				return
			}
		}
	}()
	res := <-resultCh
	if res.err != nil {
		return nil, res.err
	}

	return res.ns, nil
}

type nsRequest struct {
	fn   func() error
	done chan error
}
