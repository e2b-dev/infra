package block

import (
	"fmt"

	"github.com/tklauser/go-sysconf"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// IOV_MAX is the limit of the vectors that can be passed in a single ioctl call.
var IOV_MAX = utils.Must(getIOVMax())

func getIOVMax() (int, error) {
	iovMax, err := sysconf.Sysconf(sysconf.SC_IOV_MAX)
	if err != nil {
		return 0, fmt.Errorf("failed to get IOV_MAX: %w", err)
	}

	return int(iovMax), nil
}
