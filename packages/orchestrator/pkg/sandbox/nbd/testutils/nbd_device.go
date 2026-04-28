package testutils

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/nbdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// GetNBDDevice re-exports nbdutil.GetNBDDevice for backward compatibility.
func GetNBDDevice(ctx context.Context, backend block.Device, featureFlags *featureflags.Client, mountOpts ...nbd.MountOption) (nbd.DevicePath, *nbdutil.Cleaner, error) {
	return nbdutil.GetNBDDevice(ctx, backend, featureFlags, mountOpts...)
}
