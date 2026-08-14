//go:build linux

package v2

import (
	"errors"
	"fmt"
	"os"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// appendUnlessAbsent records err unless it only reports state that is already
// gone, which makes teardown idempotent.
func appendUnlessAbsent(errs *[]error, err error, isAbsent func(error) bool, format string) {
	if err == nil || isAbsent(err) {
		return
	}

	*errs = append(*errs, fmt.Errorf(format, err))
}

type notExistError interface {
	IsNotExist() bool
}

func isIPTablesNotExist(err error) bool {
	var notExist notExistError

	return errors.As(err, &notExist) && notExist.IsNotExist()
}

func isRouteNotExist(err error) bool {
	return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT)
}

func isLinkNotExist(err error) bool {
	var linkNotFound netlink.LinkNotFoundError

	return errors.As(err, &linkNotFound) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ENOENT)
}

func isNamespaceNotExist(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, unix.ENOENT)
}
