package template

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type MaskTemplate struct {
	template Template

	memfile *block.ReadonlyDevice
}

type MaskTemplateOption func(*MaskTemplate)

func WithMemfile(memfile block.ReadonlyDevice) MaskTemplateOption {
	return func(c *MaskTemplate) {
		c.memfile = &memfile
	}
}

func NewMaskTemplate(
	template Template,
	opts ...MaskTemplateOption,
) *MaskTemplate {
	t := &MaskTemplate{
		template: template,
		memfile:  nil,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (c *MaskTemplate) Close() error {
	if c.memfile != nil {
		return (*c.memfile).Close()
	}

	return nil
}

func (c *MaskTemplate) Files() storage.TemplateCacheFiles {
	return c.template.Files()
}

func (c *MaskTemplate) Memfile() (block.ReadonlyDevice, error) {
	if c.memfile != nil {
		return *c.memfile, nil
	}
	return c.template.Memfile()
}

func (c *MaskTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return c.template.Rootfs()
}

func (c *MaskTemplate) Snapfile() (File, error) {
	return c.template.Snapfile()
}

func (c *MaskTemplate) Metadata() (metadata.Template, error) {
	return c.template.Metadata()
}
