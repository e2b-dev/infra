package testutils

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	feature_flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func GetNBDDevice(ctx context.Context, backend block.Device, featureFlags *feature_flags.Client) (nbd.DevicePath, *Cleaner, error) {
	var cleaner Cleaner

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return "", &cleaner, fmt.Errorf("failed to create device pool: %w", err)
	}

	poolClosed := make(chan struct{})

	cleaner.Add(func(cleanupCtx context.Context) error {
		<-poolClosed

		err = devicePool.Close(cleanupCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close device pool: %v\n", err)
		}

		return nil
	})

	poolCtx, poolCancel := context.WithCancel(ctx)

	cleaner.Add(func(context.Context) error {
		poolCancel()

		return nil
	})

	go func() {
		devicePool.Populate(poolCtx)
		close(poolClosed)
	}()

	mnt := nbd.NewDirectPathMount(backend, devicePool, featureFlags)

	mntIndex, err := mnt.Open(ctx)
	if err != nil {
		return "", &cleaner, fmt.Errorf("failed to open nbd mount: %w", err)
	}

	cleaner.Add(func(cleanupCtx context.Context) error {
		err = mnt.Close(cleanupCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close nbd mount: %v\n", err)
		}

		return nil
	})

	return nbd.GetDevicePath(mntIndex), &cleaner, nil
}
