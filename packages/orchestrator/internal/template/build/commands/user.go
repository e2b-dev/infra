package commands

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type User struct{}

var _ Command = (*User)(nil)

func (u *User) Execute(
	ctx context.Context,
	logger *zap.Logger,
	lvl zapcore.Level,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	args := step.GetArgs()
	// args: [username, optional_add_to_sudo]
	if len(args) < 1 {
		return metadata.Context{}, fmt.Errorf("USER requires a username argument")
	}

	userArg := args[0]

	// Check if user already exists
	err := sandboxtools.RunCommand(
		ctx,
		proxy,
		sandboxID,
		fmt.Sprintf("id -u %s", userArg),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	userExists := err == nil

	// Only create user if it doesn't exist
	if !userExists {
		err = sandboxtools.RunCommandWithLogger(
			ctx,
			proxy,
			logger,
			lvl,
			prefix,
			sandboxID,
			fmt.Sprintf("adduser --disabled-password --gecos \"\" %s", userArg),
			metadata.Context{
				User:    "root",
				EnvVars: cmdMetadata.EnvVars,
			},
		)
		if err != nil {
			return metadata.Context{}, fmt.Errorf("failed to create user: %w", err)
		}
	}

	if len(args) > 1 && args[1] == "true" {
		cmdMetadata, err = addToSudoers(ctx, logger, proxy, sandboxID, prefix, zapcore.DebugLevel, cmdMetadata, userArg)
		if err != nil {
			return metadata.Context{}, err
		}
	}

	return saveUserMeta(ctx, proxy, sandboxID, cmdMetadata, userArg)
}

func addToSudoers(
	ctx context.Context,
	logger *zap.Logger,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	lvl zapcore.Level,
	cmdMetadata metadata.Context,
	userArg string,
) (metadata.Context, error) {
	// Add user to sudo group
	err := sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		lvl,
		prefix,
		sandboxID,
		fmt.Sprintf("usermod -aG sudo %s", userArg),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to add user to sudo group: %w", err)
	}

	// Remove password
	err = sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		lvl,
		prefix,
		sandboxID,
		fmt.Sprintf("passwd -d %s", userArg),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to remove user password: %w", err)
	}

	// Add to sudoers if not already present
	err = sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		lvl,
		prefix,
		sandboxID,
		fmt.Sprintf("grep -q '^%s ALL=(ALL:ALL) NOPASSWD: ALL' /etc/sudoers || echo '%s ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers", userArg, userArg),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to configure sudoers: %w", err)
	}

	return cmdMetadata, nil
}

func saveUserMeta(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata metadata.Context,
	user string,
) (metadata.Context, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, user),
		metadata.Context{
			User: "root",
		},
		func(stdout, _ string) {
			user = stdout
		},
	)

	cmdMetadata.User = user

	return cmdMetadata, err
}
