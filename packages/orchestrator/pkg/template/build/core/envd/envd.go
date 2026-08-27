package envd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func GetEnvdVersion(ctx context.Context, envdPath string) (string, error) {
	cmd := exec.CommandContext(ctx, envdPath, "-version")
	// A binary on a broken mount can hang in uninterruptible I/O where even
	// the ctx-deadline SIGKILL doesn't reap it; WaitDelay makes Output return
	// once the ctx is done anyway instead of waiting on the corpse forever.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error while getting envd version: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
