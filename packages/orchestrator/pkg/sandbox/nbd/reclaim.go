//go:build linux

package nbd

import (
	"context"
	"fmt"
)

// ReclaimLeaked disconnects every currently-connected NBD device left over from
// a previous orchestrator run. It returns the number of devices disconnected and
// any per-device failures.
func ReclaimLeaked(ctx context.Context) (int, []error) {
	devices, err := ConnectedDevices()
	if err != nil {
		return 0, []error{err}
	}

	reclaimed := 0
	var failures []error
	for _, device := range devices {
		if err := DisconnectDevice(ctx, device); err != nil {
			failures = append(failures, fmt.Errorf("failed to disconnect nbd%d: %w", device, err))

			continue
		}

		reclaimed++
	}

	return reclaimed, failures
}
