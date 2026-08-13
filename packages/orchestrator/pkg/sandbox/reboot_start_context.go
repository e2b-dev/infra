package sandbox

import (
	"maps"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
)

func rebootStartContext(meta metadata.Template) (string, *string, map[string]string, string, bool) {
	if meta.Start == nil || meta.Start.StartCmd == "" {
		return "", nil, nil, "", false
	}

	envs := maps.Clone(meta.Start.Context.EnvVars)
	if path, ok := envs["PATH"]; ok {
		envs["PATH"] = path + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	return meta.Start.StartCmd, meta.Start.Context.WorkDir, envs, meta.Start.Context.User, true
}
