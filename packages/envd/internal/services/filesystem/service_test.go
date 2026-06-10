package filesystem

import (
	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func mockService() Service {
	logger := zerolog.Nop()

	return Service{
		logger:   &logger,
		watchers: utils.NewMap[string, *FileWatcher](),
		defaults: &execcontext.Defaults{
			EnvVars: utils.NewEnvVars(),
		},
	}
}
