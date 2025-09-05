package commands

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Env struct{}

var _ Command = (*Env)(nil)

func (e *Env) Execute(
	ctx context.Context,
	logger *zap.Logger,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	cmdType := strings.ToUpper(step.Type)
	args := step.Args
	// args: [key1 value1 key2 value2 ...]
	if len(args) == 0 {
		return metadata.Context{}, fmt.Errorf("%s does not support passing no arguments", cmdType)
	}

	if len(args)%2 != 0 {
		return metadata.Context{}, fmt.Errorf("%s requires both a key and value arguments", cmdType)
	}

	envVars := maps.Clone(cmdMetadata.EnvVars)
	for i := 0; i < len(args)-1; i += 2 {
		k := args[i]
		v, err := evaluateValue(ctx, proxy, sandboxID, args[i+1])
		if err != nil {
			return metadata.Context{}, fmt.Errorf("failed to evaluate environment variable %s: %w", k, err)
		}

		envVars[k] = v
	}

	cmdMetadata.EnvVars = envVars
	return cmdMetadata, nil
}

func evaluateValue(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	envValue string,
) (string, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, envValue),
		metadata.Context{
			User: "root",
		},
		func(stdout, stderr string) {
			envValue = stdout
		},
	)
	if err != nil {
		return "", err
	}

	return envValue, nil
}
