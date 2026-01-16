package nfs

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

type errorReporter struct {
	inner nfs.Handler
}

var _ nfs.Handler = (*errorReporter)(nil)

func newErrorReporter(handler nfs.Handler) nfs.Handler {
	return errorReporter{inner: handler}
}

func (e errorReporter) Mount(ctx context.Context, conn net.Conn, request nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	e.logStart("Mount")
	defer e.logEnd("Mount")

	s, fs, auth := e.inner.Mount(ctx, conn, request)
	e.logMaybe("Mount", maybeStatusOK(s))
	return s, fs, auth
}

func maybeStatusOK(s nfs.MountStatus) error {
	if s == nfs.MountStatusOk {
		return nil
	}

	return fmt.Errorf("mount status = %d", s)
}

func (e errorReporter) Change(filesystem billy.Filesystem) billy.Change {
	e.logStart("Change")
	defer e.logEnd("Change")

	return e.inner.Change(filesystem)
}

func (e errorReporter) FSStat(ctx context.Context, filesystem billy.Filesystem, stat *nfs.FSStat) error {
	e.logStart("FSStat")
	defer e.logEnd("FSStat")

	err := e.inner.FSStat(ctx, filesystem, stat)
	e.logMaybe("FSStat", err)
	return err
}

func (e errorReporter) ToHandle(fs billy.Filesystem, path []string) []byte {
	e.logStart("Change")
	defer e.logEnd("Change")

	return e.inner.ToHandle(fs, path)
}

func (e errorReporter) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	e.logStart("FromHandle")
	defer e.logEnd("FromHandle")

	fs, paths, err := e.inner.FromHandle(fh)
	e.logMaybe("FromHandle", err)
	return fs, paths, err
}

func (e errorReporter) InvalidateHandle(filesystem billy.Filesystem, bytes []byte) error {
	e.logStart("InvalidateHandle")
	defer e.logEnd("InvalidateHandle")

	err := e.inner.InvalidateHandle(filesystem, bytes)
	e.logMaybe("InvalidateHandle", err)
	return err
}

func (e errorReporter) HandleLimit() int {
	e.logStart("HandleLimit")
	defer e.logEnd("HandleLimit")

	return e.inner.HandleLimit()
}

func (e errorReporter) logMaybe(s string, err error) {
	if err != nil {
		slog.Warn(fmt.Sprintf("Error in %s", s), "error", err)
	}
}

func (e errorReporter) logStart(s string) {
	slog.Debug("Starting " + s)
}

func (e errorReporter) logEnd(s string) {
	slog.Debug("Finishing " + s)
}
