//go:build linux

package commands

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type User struct{}

var _ Command = (*User)(nil)

func (u *User) Execute(
	ctx context.Context,
	logger logger.Logger,
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
		return metadata.Context{}, errors.New("USER requires a username argument")
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
			// useradd is in shadow(-utils) on every supported distro family; the
			// created user has no password (locked).
			fmt.Sprintf("useradd --create-home --shell /bin/bash %s", userArg),
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
	logger logger.Logger,
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
		// Admin group differs by distro (sudo on Debian/Ubuntu, wheel on
		// RHEL/Arch/Alpine); the NOPASSWD sudoers entry below is what actually
		// grants privileges. Neither group existing is a real error.
		fmt.Sprintf(`if getent group sudo >/dev/null; then
    usermod -aG sudo %[1]s
elif getent group wheel >/dev/null; then
    usermod -aG wheel %[1]s
else
    echo "neither the sudo nor the wheel group exists on this image" >&2
    exit 1
fi`, userArg),
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
