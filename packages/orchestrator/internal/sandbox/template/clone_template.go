package template

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type CloneTemplate struct {
	template Template

	memfile *block.ReadonlyDevice
}

type CloneTemplateOption func(*CloneTemplate)

func WithMemfile(memfile block.ReadonlyDevice) CloneTemplateOption {
	return func(c *CloneTemplate) {
		c.memfile = &memfile
	}
}

func NewCloneTemplate(
	template Template,
	opts ...CloneTemplateOption,
) *CloneTemplate {
	t := &CloneTemplate{
		template: template,
		memfile:  nil,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (c *CloneTemplate) Close() error {
	if c.memfile != nil {
		return (*c.memfile).Close()
	}

	return nil
}

func (c *CloneTemplate) Files() storage.TemplateCacheFiles {
	return c.template.Files()
}

func (c *CloneTemplate) Memfile() (block.ReadonlyDevice, error) {
	if c.memfile != nil {
		return *c.memfile, nil
	}
	return c.template.Memfile()
}

func (c *CloneTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return c.template.Rootfs()
}

func (c *CloneTemplate) Snapfile() (File, error) {
	return c.template.Snapfile()
}

func (c *CloneTemplate) Metadata() (metadata.Template, error) {
	return c.template.Metadata()
}
