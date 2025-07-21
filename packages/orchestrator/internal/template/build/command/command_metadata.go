package command

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
)

var cmdMetadataBaseDirPath = filepath.Join("/etc", "e2b-metadata")

func CleanCommandMetadata(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	return sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`rm -rf "%s"`, cmdMetadataBaseDirPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}

func ReadCommandMetadata(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	baseCmdMetadata sandboxtools.CommandMetadata,
) (sandboxtools.CommandMetadata, error) {
	user := baseCmdMetadata.User
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`[ -f "%s" ] && cat "%s" || echo ""`, userPath, userPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
		func(stdout, stderr string) {
			w := strings.TrimSpace(stdout)
			if w != "" {
				user = w
			}
		},
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to get current user: %w", err)
	}

	workdir := baseCmdMetadata.WorkDir
	err = sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`[ -f "%s" ] && cat "%s" || echo ""`, workdirPath, workdirPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
		func(stdout, stderr string) {
			w := strings.TrimSpace(stdout)
			if w != "" {
				workdir = &w
			}
		},
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to get current user: %w", err)
	}

	envPath := fmt.Sprintf("%s/%s", envPathPrefix, user)
	envVars := baseCmdMetadata.EnvVars
	err = sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`[ -f "%s" ] && cat "%s" || echo ""`, envPath, envPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
		func(stdout, stderr string) {
			// Parse env vars
			lines := strings.Split(stdout, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				envParts := strings.SplitN(line, "=", 2)
				envVars[envParts[0]] = envParts[1]
			}
		},
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to get environment variables: %w", err)
	}

	return sandboxtools.CommandMetadata{
		User:    user,
		WorkDir: workdir,
		EnvVars: envVars,
	}, nil
}
