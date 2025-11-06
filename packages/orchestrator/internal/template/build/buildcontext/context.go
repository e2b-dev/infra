package buildcontext

import (
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
)

type BuildContext struct {
	Config         config.TemplateConfig
	BuilderConfig  cfg.BuilderConfig
	Template       paths.TemplateFiles
	UploadErrGroup *errgroup.Group
	EnvdVersion    string
	CacheScope     string
	IsV1Build      bool
	Version        string
}
