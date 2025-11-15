package buildcontext

import (
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type BuildContext struct {
	BuilderConfig  cfg.BuilderConfig
	Config         config.TemplateConfig
	Template       storage.TemplateFiles
	UploadErrGroup *errgroup.Group
	EnvdVersion    string
	CacheScope     string
	IsV1Build      bool
	Version        string
}
