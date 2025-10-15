package filesystem

import (
	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func mockService() Service {
	return Service{
		defaults: &execcontext.Defaults{
			EnvVars: utils.NewMap[string, string](),
		},
	}
}
