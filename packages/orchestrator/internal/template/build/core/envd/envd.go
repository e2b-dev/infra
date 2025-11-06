package envd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
)

func GetEnvdVersion(ctx context.Context, config cfg.BuilderConfig) (string, error) {
	cmd := exec.CommandContext(ctx, config.HostEnvdPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error while getting envd version: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
