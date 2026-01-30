package commands

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Env struct{}

var _ Command = (*Env)(nil)

func (e *Env) Execute(
	ctx context.Context,
	_ logger.Logger,
	_ zapcore.Level,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	_ string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	cmdType := strings.ToUpper(step.GetType())
	args := step.GetArgs()
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

// shellEscape escapes a string for safe use in shell single quotes.
// Single-quoted strings in bash don't interpret any special characters,
// so we only need to handle the single quote itself.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func evaluateValue(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	envValue string,
) (string, error) {
	// Use printf with single-quote escaping for safe input,
	// then pipe through envsubst to expand environment variables.
	cmd := fmt.Sprintf("printf '%%s' %s | envsubst", shellEscape(envValue))

	err := sandboxtools.RunCommandWithOutput(
		ctx,
		proxy,
		sandboxID,
		cmd,
		metadata.Context{
			User: "root",
		},
		func(stdout, _ string) {
			envValue = stdout
		},
	)
	if err != nil {
		return "", err
	}

	return envValue, nil
}
