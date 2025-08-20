package commands

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type User struct{}

func (u *User) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	args := step.Args
	// args: [username]
	if len(args) < 1 {
		return metadata.Context{}, fmt.Errorf("USER requires a username argument")
	}

	userArg := args[0]

	err := sandboxtools.RunCommandWithLogger(
		ctx,
		tracer,
		proxy,
		postProcessor,
		zapcore.InfoLevel,
		prefix,
		sandboxID,
		fmt.Sprintf("adduser -disabled-password --gecos \"\" %s || true", userArg),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to create user: %w", err)
	}

	return saveUserMeta(ctx, tracer, proxy, sandboxID, cmdMetadata, userArg)
}

func saveUserMeta(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata metadata.Context,
	user string,
) (metadata.Context, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, user),
		metadata.Context{
			User: "root",
		},
		func(stdout, stderr string) {
			user = stdout
		},
	)

	cmdMetadata.User = user
	return cmdMetadata, err
}
