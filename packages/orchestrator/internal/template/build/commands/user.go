package commands

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
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
	cmdMetadata sandboxtools.CommandMetadata,
) (sandboxtools.CommandMetadata, error) {
	args := step.Args
	// args: [username]
	if len(args) < 1 {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("USER requires a username argument")
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
		sandboxtools.CommandMetadata{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("create user: %w", err)
	}

	return saveUserMeta(ctx, tracer, proxy, sandboxID, cmdMetadata, userArg)
}

func saveUserMeta(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata sandboxtools.CommandMetadata,
	user string,
) (sandboxtools.CommandMetadata, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, user),
		sandboxtools.CommandMetadata{
			User: "root",
		},
		func(stdout, stderr string) {
			user = stdout
		},
	)

	cmdMetadata.User = user
	return cmdMetadata, err
}
