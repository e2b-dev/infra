package sandbox

import (
	"context"
	"maps"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
)

// rebootStartControlContext bounds the envd start handshake without exposing a
// deadline to Connect. Connect serializes a context deadline as
// Connect-Timeout-Ms, which envd intentionally treats as the lifetime of the
// started process. The replayed template command owns the sandbox process
// graph, so applying the handshake deadline to it tears the runtime down after
// rebootStartCommandTimeout.
func rebootStartControlContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	stopParent := context.AfterFunc(parent, cancel)
	timer := time.AfterFunc(timeout, cancel)

	return ctx, func() {
		stopParent()
		timer.Stop()
		cancel()
	}
}

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
