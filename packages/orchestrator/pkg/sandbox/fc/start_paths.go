//go:build linux

package fc

import (
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
)

// StartPathsResult contains mount namespace paths used by fc-nsenter when
// preparing Firecracker's rootfs and kernel links.
type StartPathsResult struct {
	// RootfsPath is the computed rootfs path (with backward compatibility)
	RootfsPath string

	// KernelPath is the computed kernel path
	KernelPath string
}

// StartPathsBuilder computes paths that fc-nsenter will materialize inside the
// sandbox mount namespace before Firecracker starts.
type StartPathsBuilder struct {
	builderConfig cfg.BuilderConfig
}

// NewStartPathsBuilder creates a StartPathsBuilder from the orchestrator
// builder configuration.
func NewStartPathsBuilder(builderConfig cfg.BuilderConfig) *StartPathsBuilder {
	return &StartPathsBuilder{
		builderConfig: builderConfig,
	}
}

// Build returns rootfs and kernel link paths for the provided Firecracker and
// rootfs layout versions.
func (sb *StartPathsBuilder) Build(
	versions Config,
	rootfsPaths RootfsPaths,
) *StartPathsResult {
	return &StartPathsResult{
		RootfsPath: sb.rootfsPath(rootfsPaths),
		KernelPath: sb.kernelPath(versions),
	}
}

func (sb *StartPathsBuilder) rootfsPath(rootfsPaths RootfsPaths) string {
	rootfsPath := filepath.Join(sb.builderConfig.SandboxDir, SandboxRootfsFile)
	if rootfsPaths.TemplateVersion <= 1 {
		rootfsPath = filepath.Join(rootfsPaths.DeprecatedSandboxRootfsDir(), SandboxRootfsFile)
	}

	return rootfsPath
}

func (sb *StartPathsBuilder) kernelPath(versions Config) string {
	return filepath.Join(sb.builderConfig.SandboxDir, versions.SandboxKernelDir(), SandboxKernelFile)
}
