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
		// Try Debian-style adduser first, fall back to useradd for RHEL/CentOS/Alpine
		createUserCmd := buildCreateUserCmd(userArg)
		err = sandboxtools.RunCommandWithLogger(
			ctx,
			proxy,
			logger,
			lvl,
			prefix,
			sandboxID,
			createUserCmd,
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
	// Add user to sudo/wheel group (sudo for Debian/Ubuntu, wheel for RHEL/CentOS/Alpine)
	err := sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		lvl,
		prefix,
		sandboxID,
		buildAddToGroupCmd(userArg),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to add user to sudo group: %w", err)
	}

	// Remove password (passwd may not exist on minimal images)
	err = sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		lvl,
		prefix,
		sandboxID,
		buildRemovePasswordCmd(userArg),
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
		buildSudoersCmd(userArg),
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

// buildCreateUserCmd returns the shell command that creates a user, trying
// Debian-style adduser first, then useradd (RHEL/CentOS), then Alpine adduser.
func buildCreateUserCmd(username string) string {
	return fmt.Sprintf(
		"adduser --disabled-password --gecos \"\" %s 2>/dev/null || useradd -m %s 2>/dev/null || adduser -D %s",
		username, username, username,
	)
}

// buildAddToGroupCmd returns the shell command that adds a user to the sudo or
// wheel group, depending on the distro.
func buildAddToGroupCmd(username string) string {
	return fmt.Sprintf("usermod -aG sudo %s 2>/dev/null || usermod -aG wheel %s 2>/dev/null || true", username, username)
}

// buildRemovePasswordCmd returns the shell command that removes a user's password.
func buildRemovePasswordCmd(username string) string {
	return fmt.Sprintf("passwd -d %s 2>/dev/null || true", username)
}

// buildSudoersCmd returns the shell command that appends a NOPASSWD sudoers
// entry for the given user if it is not already present.
func buildSudoersCmd(username string) string {
	return fmt.Sprintf(
		"touch /etc/sudoers && (grep -q '^%s ALL=(ALL:ALL) NOPASSWD: ALL' /etc/sudoers || echo '%s ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers)",
		username, username,
	)
}
