package command

import (
	"context"
	"fmt"
	"path/filepath"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var userPath = filepath.Join(cmdMetadataBaseDirPath, "user")

type User struct{}

func (u *User) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	templateID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata sandboxtools.CommandMetadata,
) error {
	args := step.Args
	// args: [username]
	if len(args) < 1 {
		return fmt.Errorf("USER requires a username argument")
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
		"adduser "+userArg,
		sandboxtools.CommandMetadata{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create user in sandbox: %w", err)
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
) error {
	return sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "$(dirname "%s")" && echo "%s" > "%s"`, userPath, user, userPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}
