package host

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

var syncingLock sync.RWMutex

const syncTimeout = 2 * time.Second

func updateClock() error {
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/bash", "-l", "-c", "date -s @$(/usr/sbin/phc_ctl /dev/ptp0 get | cut -d' ' -f5)")

	// Capture both stdout and stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to update clock: %w\nCommand output: %s", err, string(output))
	}

	return nil
}

func Sync() error {
	syncingLock.Lock()
	defer syncingLock.Unlock()

	err := updateClock()
	if err != nil {
		return fmt.Errorf("failed to sync clock: %w", err)
	}

	return nil
}

func WaitForSync() {
	syncingLock.RLock()
	syncingLock.RUnlock()
}
