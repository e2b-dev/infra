package build

import (
	"io"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateConfig struct {
	*storage.TemplateFiles

	// Command to run when building the template.
	StartCmd string

	// The number of vCPUs to allocate to the VM.
	VCpuCount int64

	// The amount of RAM memory to allocate to the VM, in MiB.
	MemoryMB int64

	// The amount of free disk to allocate to the VM, in MiB.
	DiskSizeMB int64

	// Path to the directory where the temporary files for the build are stored.
	BuildLogsWriter io.Writer

	// Real size of the rootfs after building the template.
	rootfsSize int64

	// HugePages sets whether the VM use huge pages.
	HugePages bool
}

// Real size in MB of rootfs after building the template
func (e *TemplateConfig) RootfsSizeMB() int64 {
	return e.rootfsSize >> 20
}

func (e *TemplateConfig) MemfilePageSize() int64 {
	if e.HugePages {
		return header.HugepageSize
	}

	return header.PageSize
}

func (e *TemplateConfig) RootfsBlockSize() int64 {
	return header.RootfsBlockSize
}

func (e *TemplateConfig) ToSandboxConfig(envdVersion string) *orchestrator.SandboxConfig {
	return &orchestrator.SandboxConfig{
		TemplateId:         e.TemplateId,
		BuildId:            e.BuildId,
		KernelVersion:      e.KernelVersion,
		FirecrackerVersion: e.FirecrackerVersion,
		HugePages:          e.HugePages,
		SandboxId:          uuid.New().String(),

		EnvdVersion: envdVersion,
		Vcpu:        e.VCpuCount,
		RamMb:       e.MemoryMB,

		BaseTemplateId: e.TemplateId,
	}
}
