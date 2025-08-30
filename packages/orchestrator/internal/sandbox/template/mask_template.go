package template

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type MaskTemplate struct {
	template Template

	memfile *block.ReadonlyDevice
}

var _ Template = (*MaskTemplate)(nil)

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

func (c *MaskTemplate) Close(ctx context.Context) error {
	if c.memfile != nil {
		return (*c.memfile).Close()
	}

	return nil
}

func (c *MaskTemplate) Files() storage.TemplateCacheFiles {
	return c.template.Files()
}

func (c *MaskTemplate) Memfile(ctx context.Context) (block.ReadonlyDevice, error) {
	if c.memfile != nil {
		return *c.memfile, nil
	}
	return c.template.Memfile(ctx)
}

func (c *MaskTemplate) Rootfs(ctx context.Context) (block.ReadonlyDevice, error) {
	return c.template.Rootfs(ctx)
}

func (c *MaskTemplate) Snapfile(ctx context.Context) (File, error) {
	return c.template.Snapfile(ctx)
}

func (c *MaskTemplate) Metadata(ctx context.Context) (metadata.Template, error) {
	return c.template.Metadata(ctx)
}
