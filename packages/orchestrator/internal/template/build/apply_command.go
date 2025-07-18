package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/utils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var (
	metadataDirPath = filepath.Join("tmp", "metadata")

	workdirPath   = filepath.Join(metadataDirPath, "workdir")
	userPath      = filepath.Join(metadataDirPath, "user")
	envPathPrefix = filepath.Join(metadataDirPath, "env")
)

func (b *Builder) applyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	templateID string,
	sbx *sandbox.Sandbox,
	prefix string,
	step *templatemanager.TemplateStep,
	baseCmdMetadata sandboxtools.CommandMetadata,
) error {
	ctx, span := b.tracer.Start(ctx, "apply-command", trace.WithAttributes(
		attribute.String("prefix", prefix),
		attribute.String("sandbox.id", sbx.Metadata.Config.SandboxId),
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.files.hash", utils.Sprintp(step.FilesHash)),
	))
	defer span.End()

	cmdMetadata, err := b.readCommandMetadata(ctx, sbx.Metadata.Config.SandboxId, baseCmdMetadata)
	if err != nil {
		return fmt.Errorf("failed to read command metadata: %w", err)
	}

	cmdType := strings.ToUpper(step.Type)
	args := step.Args

	switch cmdType {
	case "ADD", "COPY":
		// args: [localPath containerPath]
		if len(args) < 2 {
			return fmt.Errorf("%s requires a local path and a container path argument", cmdType)
		}

		if step.FilesHash == nil || *step.FilesHash == "" {
			return fmt.Errorf("%s requires files hash to be set", cmdType)
		}

		obj, err := b.buildStorage.OpenObject(ctx, GetLayerFilesCachePath(templateID, *step.FilesHash))
		if err != nil {
			return fmt.Errorf("failed to open files object from storage: %w", err)
		}

		pr, pw := io.Pipe()
		// Start writing tar data to the pipe writer in a goroutine
		go func() {
			defer pw.Close()
			if _, err := obj.WriteTo(pw); err != nil {
				pw.CloseWithError(err)
			}
		}()

		tmpFile, err := os.CreateTemp("", "layer-file-*.tar")
		if err != nil {
			return fmt.Errorf("failed to create temporary file for layer tar: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		_, err = io.Copy(tmpFile, pr)
		if err != nil {
			return fmt.Errorf("failed to copy layer tar data to temporary file: %w", err)
		}

		// The file is automatically cleaned up by the sandbox restart in the last step.
		// This is happening because the /tmp is mounted as a tmpfs and deleted on restart.
		sbxTargetPath := filepath.Join("/tmp", fmt.Sprintf("%s.tar", *step.FilesHash))
		err = sandboxtools.CopyFile(ctx, b.tracer, b.proxy, sbx.Metadata.Config.SandboxId, cmdMetadata.User, tmpFile.Name(), sbxTargetPath)
		if err != nil {
			return fmt.Errorf("failed to copy layer tar data to sandbox: %w", err)
		}

		sbxUnpackPath := filepath.Join("/tmp", *step.FilesHash)

		err = sandboxtools.RunCommandWithLogger(
			ctx,
			b.tracer,
			b.proxy,
			nil,
			zapcore.DebugLevel,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`mkdir -p "%s" && tar -xzvf "%s" -C "%s"`, sbxUnpackPath, sbxTargetPath, sbxUnpackPath),
			cmdMetadata,
		)
		if err != nil {
			return fmt.Errorf("failed to extract files in sandbox: %w", err)
		}

		moveScript := fmt.Sprintf(`
#!/bin/bash

# Get the parent folder of the source file/folder
sourceFolder="$(dirname "%s")"

# Set targetPath relative to current working directory if not absolute
inputPath="%s"
if [[ "$inputPath" = /* ]]; then
  targetPath="$inputPath"
else
  targetPath="$(pwd)/$inputPath"
fi

cd "$sourceFolder" || exit 1

entry=$(ls -A | head -n 1)

if [ -z "$entry" ]; then
  echo "Error: sourceFolder is empty"
  exit 1
fi

if [ -f "$entry" ]; then
  # It's a file – create parent folders and move+rename it to the exact path
  mkdir -p "$(dirname "$targetPath")"
  mv "$entry" "$targetPath"
elif [ -d "$entry" ]; then
  # It's a directory – move all its contents into the destination folder
  mkdir -p "$targetPath"
  mv "$entry"/* "$targetPath/"
else
  echo "Error: entry is neither file nor directory"
  exit 1
fi
`, filepath.Join(sbxUnpackPath, args[0]), args[1])

		err = sandboxtools.RunCommandWithLogger(
			ctx,
			b.tracer,
			b.proxy,
			nil,
			zapcore.DebugLevel,
			prefix,
			sbx.Metadata.Config.SandboxId,
			moveScript,
			cmdMetadata,
		)
		if err != nil {
			return fmt.Errorf("failed to move files in sandbox: %w", err)
		}

		return nil
	case "RUN":
		// args: command and args, e.g., ["sh", "-c", "echo hi"]
		if len(args) < 1 {
			return fmt.Errorf("RUN requires command arguments")
		}

		cmd := strings.Join(args, " ")
		return sandboxtools.RunCommandWithLogger(
			ctx,
			b.tracer,
			b.proxy,
			postProcessor,
			zapcore.InfoLevel,
			prefix,
			sbx.Metadata.Config.SandboxId,
			cmd,
			cmdMetadata,
		)
	case "USER":
		// args: [username]
		if len(args) < 1 {
			return fmt.Errorf("USER requires a username argument")
		}

		userArg := args[0]

		err = sandboxtools.RunCommandWithLogger(
			ctx,
			b.tracer,
			b.proxy,
			postProcessor,
			zapcore.InfoLevel,
			prefix,
			sbx.Metadata.Config.SandboxId,
			"adduser "+userArg,
			sandboxtools.CommandMetadata{
				User:    "root",
				EnvVars: cmdMetadata.EnvVars,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create user in sandbox: %w", err)
		}

		return b.saveUser(ctx, sbx.Metadata.Config.SandboxId, cmdMetadata, userArg)
	case "WORKDIR":
		// args: [path]
		if len(args) < 1 {
			return fmt.Errorf("WORKDIR requires a path argument")
		}

		workdirArg := args[0]

		err = sandboxtools.RunCommandWithLogger(
			ctx,
			b.tracer,
			b.proxy,
			postProcessor,
			zapcore.InfoLevel,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`mkdir -p "%s"`, workdirArg),
			sandboxtools.CommandMetadata{
				User:    cmdMetadata.User,
				EnvVars: cmdMetadata.EnvVars,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create workdir in sandbox: %w", err)
		}

		return b.saveWorkdir(ctx, sbx.Metadata.Config.SandboxId, cmdMetadata, workdirArg)
	case "ENV", "ARG":
		// args: [key value]
		if len(args) < 2 {
			return fmt.Errorf("%s requires a key and value argument", cmdType)
		}

		return b.saveEnv(ctx, sbx.Metadata.Config.SandboxId, cmdMetadata, args[0], args[1])
	default:
		return fmt.Errorf("unsupported command type: %s", cmdType)
	}
}

func (b *Builder) readCommandMetadata(
	ctx context.Context,
	sandboxID string,
	baseCmdMetadata sandboxtools.CommandMetadata,
) (sandboxtools.CommandMetadata, error) {
	user := baseCmdMetadata.User
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		b.tracer,
		b.proxy,
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
		b.tracer,
		b.proxy,
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
		b.tracer,
		b.proxy,
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

func (b *Builder) saveEnv(
	ctx context.Context,
	sandboxID string,
	cmdMetadata sandboxtools.CommandMetadata,
	envName string,
	envValue string,
) error {
	envPath := fmt.Sprintf("%s/%s", envPathPrefix, cmdMetadata.User)

	return sandboxtools.RunCommand(
		ctx,
		b.tracer,
		b.proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "$(dirname "%s")" && echo "%s=%s" >> "%s"`, envPath, envName, envValue, envPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}

func (b *Builder) saveWorkdir(
	ctx context.Context,
	sandboxID string,
	cmdMetadata sandboxtools.CommandMetadata,
	workdir string,
) error {
	return sandboxtools.RunCommand(
		ctx,
		b.tracer,
		b.proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "$(dirname "%s")" && echo "%s" > "%s"`, workdirPath, workdir, workdirPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}

func (b *Builder) saveUser(
	ctx context.Context,
	sandboxID string,
	cmdMetadata sandboxtools.CommandMetadata,
	user string,
) error {
	return sandboxtools.RunCommand(
		ctx,
		b.tracer,
		b.proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "$(dirname "%s")" && echo "%s" > "%s"`, userPath, user, userPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}
