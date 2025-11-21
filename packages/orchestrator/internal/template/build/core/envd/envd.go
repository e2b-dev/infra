package envd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func GetEnvdVersion(ctx context.Context, envdPath string) (string, error) {
	cmd := exec.CommandContext(ctx, envdPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error while getting envd version: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
