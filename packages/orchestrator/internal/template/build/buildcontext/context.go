package buildcontext

import (
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type BuildContext struct {
	Config         config.TemplateConfig
	Template       storage.TemplateFiles
	UserLogger     *zap.Logger
	UploadErrGroup *errgroup.Group
	EnvdVersion    string
	CacheScope     string
	IsV1Build      bool
}
