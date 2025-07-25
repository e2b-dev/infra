package config

import (
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const InstanceBuildPrefix = "b"

type TemplateConfig struct {
	// CacheScope is the scope of layers and files caches.
	CacheScope string

	// Command to run when building the template.
	StartCmd string

	// The number of vCPUs to allocate to the VM.
	VCpuCount int64

	// The amount of RAM memory to allocate to the VM, in MiB.
	MemoryMB int64

	// The amount of free disk to allocate to the VM, in MiB.
	DiskSizeMB int64

	// HugePages sets whether the VM use huge pages.
	HugePages bool

	// Command to run to check if the template is ready.
	ReadyCmd string

	// FromImage is the base image to use for building the template.
	FromImage string

	// FromTemplate is the base template to use for building the template.
	FromTemplate string

	// Force rebuild of the template even if it is already cached.
	Force *bool

	// Steps to build the template.
	Steps []*templatemanager.TemplateStep
}

func (e TemplateConfig) MemfilePageSize() int64 {
	if e.HugePages {
		return header.HugepageSize
	}

	return header.PageSize
}

func (e TemplateConfig) RootfsBlockSize() int64 {
	return header.RootfsBlockSize
}
