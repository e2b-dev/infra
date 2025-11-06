package layer

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/paths"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
)

var _ SourceTemplateProvider = (*CacheSourceTemplateProvider)(nil)

type CacheSourceTemplateProvider struct {
	files paths.TemplateFiles
}

func NewCacheSourceTemplateProvider(
	files paths.TemplateFiles,
) *CacheSourceTemplateProvider {
	return &CacheSourceTemplateProvider{
		files: files,
	}
}

func (cstp *CacheSourceTemplateProvider) Get(ctx context.Context, templateCache *sbxtemplate.Cache) (sbxtemplate.Template, error) {
	template, err := templateCache.GetTemplate(
		ctx,
		cstp.files.BuildID,
		cstp.files.KernelVersion,
		cstp.files.FirecrackerVersion,
		false,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("get template snapshot: %w", err)
	}

	return template, nil
}

var _ SourceTemplateProvider = (*DirectSourceTemplateProvider)(nil)

type DirectSourceTemplateProvider struct {
	SourceTemplate sbxtemplate.Template
}

func NewDirectSourceTemplateProvider(template sbxtemplate.Template) *DirectSourceTemplateProvider {
	return &DirectSourceTemplateProvider{SourceTemplate: template}
}

func (dstp *DirectSourceTemplateProvider) Get(_ context.Context, _ *sbxtemplate.Cache) (sbxtemplate.Template, error) {
	return dstp.SourceTemplate, nil
}
