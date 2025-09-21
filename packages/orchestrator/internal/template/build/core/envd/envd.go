package envd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func GetEnvdVersion() (string, error) {
	cmd := exec.Command(storage.HostEnvdPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error while getting envd version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func GetEnvdHash() (string, error) {
	return utils.GetFileHash(storage.HostEnvdPath)
}
