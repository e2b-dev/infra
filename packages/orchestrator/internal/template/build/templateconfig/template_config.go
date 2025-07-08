package templateconfig

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const instanceBuildPrefix = "b"

type TemplateConfig struct {
	TemplateID         string
	BuildID            string
	KernelVersion      string
	FirecrackerVersion string

	// Command to run when building the template.
	StartCmd string

	// The number of vCPUs to allocate to the VM.
	VCpuCount int64

	// The amount of RAM memory to allocate to the VM, in MiB.
	MemoryMB int64

	// The amount of free disk to allocate to the VM, in MiB.
	DiskSizeMB int64

	// Real size of the rootfs after building the template.
	RootfsSize int64

	// HugePages sets whether the VM use huge pages.
	HugePages bool

	// Command to run to check if the template is ready.
	ReadyCmd string

	// FromImage is the base image to use for building the template.
	FromImage string

	// Force rebuild of the template even if it is already cached.
	Force *bool

	// Steps to build the template.
	Steps []*templatemanager.TemplateStep
}

// Real size in MB of rootfs after building the template
func (e *TemplateConfig) RootfsSizeMB() int64 {
	return e.RootfsSize >> 20
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
		TemplateId:         e.TemplateID,
		BuildId:            e.BuildID,
		KernelVersion:      e.KernelVersion,
		FirecrackerVersion: e.FirecrackerVersion,
		HugePages:          e.HugePages,
		SandboxId:          instanceBuildPrefix + id.Generate(),
		ExecutionId:        uuid.New().String(),
		EnvdVersion:        envdVersion,
		Vcpu:               e.VCpuCount,
		RamMb:              e.MemoryMB,

		BaseTemplateId: e.TemplateID,
	}
}
