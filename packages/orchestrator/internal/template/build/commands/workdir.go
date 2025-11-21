package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const defaultRelativeToAbsoluteWorkdir = "/"

type Workdir struct{}

var _ Command = (*Workdir)(nil)

func (w *Workdir) Execute(
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
	// args: [path]
	if len(args) < 1 {
		return metadata.Context{}, fmt.Errorf("WORKDIR requires a path argument")
	}

	workDir := defaultRelativeToAbsoluteWorkdir
	if cmdMetadata.WorkDir != nil {
		workDir = *cmdMetadata.WorkDir
	}

	workdirArg := args[0]
	if !filepath.IsAbs(workdirArg) {
		workdirArg = filepath.Join(workDir, workdirArg)
	}

	err := sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		lvl,
		prefix,
		sandboxID,
		// Use mkdir -p to create any missing parents
		// Find the first existing parent and chown from there
		// This ensures that if we have e.g. /home/user and we create /home/user/project/test
		// we only chown /home/user/project (including /home/user/project/test) and not /home or /home/user
		fmt.Sprintf(`
			target="%s"

			# Exit early if target already exists
			if [ -d "$target" ]; then
			    exit 0
			fi

			# Find the first non-existent parent
			first_new=""
			check="$target"
			while [ ! -d "$check" ]; do
			    first_new="$check"
			    check=$(dirname "$check")
			done

			# Create and chown from the top (only if we found new directories to create)
			if [ -n "$first_new" ]; then
			    mkdir -p "$target" && chown -R %s:%s "$first_new"
			fi
    `, workdirArg, cmdMetadata.User, cmdMetadata.User),
		metadata.Context{
			User:    "root",
			EnvVars: cmdMetadata.EnvVars,
			// Workdir can't be set here to not error when current workdir is deleted
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to create workdir: %w", err)
	}

	return saveWorkdirMeta(ctx, proxy, sandboxID, cmdMetadata, workdirArg)
}

func saveWorkdirMeta(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata metadata.Context,
	workdir string,
) (m metadata.Context, e error) {
	var outputErr error
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		proxy,
		sandboxID,
		fmt.Sprintf(`cd "%s" && pwd`, workdir),
		metadata.Context{
			User: "root",
			// Workdir can't be set here to not error when current workdir is deleted
		},
		func(stdout, stderr string) {
			if stderr != "" {
				outputErr = fmt.Errorf("error getting absolute path of workdir: %s", stderr)

				return
			}
			workdir = strings.TrimSpace(stdout)
		},
	)
	if outputErr != nil {
		return metadata.Context{}, outputErr
	}

	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to save workdir %s: %w", workdir, err)
	}

	cmdMetadata.WorkDir = &workdir

	return cmdMetadata, nil
}
