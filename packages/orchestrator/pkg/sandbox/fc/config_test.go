package fc

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestFirecrackerPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := utils.TargetArch()

	config := cfg.BuilderConfig{FirecrackerVersionsDir: dir}
	fc := Config{FirecrackerVersion: "v1.12.0"}

	result := fc.FirecrackerPath(config)

	assert.Equal(t, filepath.Join(dir, "v1.12.0", arch, "firecracker"), result)
}

func TestHostKernelPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := utils.TargetArch()

	config := cfg.BuilderConfig{HostKernelsDir: dir}
	fc := Config{KernelVersion: "vmlinux-6.1.102"}

	result := fc.HostKernelPath(config)

	assert.Equal(t, filepath.Join(dir, "vmlinux-6.1.102", arch, "vmlinux.bin"), result)
}
