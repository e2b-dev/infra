package testutils

import (
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/nbdutil"
)

// MountNBDDevice re-exports nbdutil.MountNBDDevice for backward compatibility.
func MountNBDDevice(device nbd.DevicePath, mountPath string) (*nbdutil.Cleaner, error) {
	return nbdutil.MountNBDDevice(device, mountPath)
}
