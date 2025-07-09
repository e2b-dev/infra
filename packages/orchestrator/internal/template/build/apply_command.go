package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/utils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (b *Builder) applyLocalCommand(
	ctx context.Context,
	step *templatemanager.TemplateStep,
	buildMetadata *buildMetadata,
) (bool, error) {
	_, span := b.tracer.Start(ctx, "apply-command-local", trace.WithAttributes(
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.hash", step.Hash),
		attribute.String("step.files.hash", utils.Sprintp(step.FilesHash)),
		attribute.String("metadata.user", buildMetadata.user),
		attribute.String("metadata.workdir", utils.Sprintp(buildMetadata.workdir)),
		attribute.String("metadata.env_vars", fmt.Sprintf("%v", buildMetadata.envVars)),
	))
	defer span.End()

	cmdType := strings.ToUpper(step.Type)
	args := step.Args

	switch cmdType {
	case "ARG":
		// args: [key value]
		if len(args) < 2 {
			return false, fmt.Errorf("ARG requires a key and value argument")
		}
		buildMetadata.envVars[args[0]] = args[1]
		return true, nil
	case "ENV":
		// args: [key value]
		if len(args) < 2 {
			return false, fmt.Errorf("ENV requires a key and value argument")
		}
		buildMetadata.envVars[args[0]] = args[1]
		return true, nil
	case "WORKDIR":
		// args: [path]
		if len(args) < 1 {
			return false, fmt.Errorf("WORKDIR requires a path argument")
		}
		cwd := args[0]
		buildMetadata.workdir = &cwd
		return false, nil
	case "USER":
		// args: [username]
		if len(args) < 1 {
			return false, fmt.Errorf("USER requires a username argument")
		}
		buildMetadata.user = args[0]
		return false, nil
	default:
		return false, nil
	}
}

func (b *Builder) applyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	templateID string,
	sbx *sandbox.Sandbox,
	prefix string,
	step *templatemanager.TemplateStep,
	buildMetadata *buildMetadata,
) error {
	ctx, span := b.tracer.Start(ctx, "apply-command", trace.WithAttributes(
		attribute.String("prefix", prefix),
		attribute.String("sandbox.id", sbx.Metadata.Config.SandboxId),
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.hash", step.Hash),
		attribute.String("step.files.hash", utils.Sprintp(step.FilesHash)),
		attribute.String("metadata.user", buildMetadata.user),
		attribute.String("metadata.workdir", utils.Sprintp(buildMetadata.workdir)),
		attribute.String("metadata.env_vars", fmt.Sprintf("%v", buildMetadata.envVars)),
	))
	defer span.End()

	cmdType := strings.ToUpper(step.Type)
	args := step.Args

	switch cmdType {
	case "ADD":
		// args: [localPath containerPath]
		fallthrough
	case "COPY":
		// args: [localPath containerPath]
		if len(args) < 2 {
			return fmt.Errorf("%s requires a local path and a container path argument", cmdType)
		}

		if step.FilesHash == nil || *step.FilesHash == "" {
			return fmt.Errorf("%s requires files hash to be set", cmdType)
		}

		obj, err := b.storage.OpenObject(ctx, GetLayerFilesCachePath(templateID, *step.FilesHash))
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
		sbxTargetPath := fmt.Sprintf("/tmp/%s.tar", *step.FilesHash)
		err = sandboxtools.CopyFile(ctx, b.tracer, b.proxy, sbx.Metadata.Config.SandboxId, buildMetadata.user, tmpFile.Name(), sbxTargetPath)
		if err != nil {
			return fmt.Errorf("failed to copy layer tar data to sandbox: %w", err)
		}

		sbxUnpackPath := fmt.Sprintf("/tmp/%s", *step.FilesHash)

		err = sandboxtools.RunCommand(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`mkdir -p "%s"`, sbxUnpackPath),
			buildMetadata.user,
			buildMetadata.workdir,
			buildMetadata.envVars,
		)
		if err != nil {
			return fmt.Errorf("failed to create directory in sandbox: %w", err)
		}

		err = sandboxtools.RunCommand(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`tar -xzvf "%s" -C "%s"`, sbxTargetPath, sbxUnpackPath),
			buildMetadata.user,
			buildMetadata.workdir,
			buildMetadata.envVars,
		)
		if err != nil {
			return fmt.Errorf("failed to extract files in sandbox: %w", err)
		}

		moveScript := fmt.Sprintf(`
#!/bin/bash

sourceFolder="%s"
# Set targetPath relative to current working directory
targetPath="$(pwd)/%s"

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
`, sbxUnpackPath, args[1])

		err = sandboxtools.RunCommand(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			moveScript,
			buildMetadata.user,
			buildMetadata.workdir,
			buildMetadata.envVars,
		)
		if err != nil {
			return fmt.Errorf("failed to extract files in sandbox: %w", err)
		}

		return nil
	case "RUN":
		// args: command and args, e.g., ["sh", "-c", "echo hi"]
		if len(args) < 1 {
			return fmt.Errorf("RUN requires command arguments")
		}

		cmd := strings.Join(args, " ")
		return sandboxtools.RunCommand(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			cmd,
			buildMetadata.user,
			buildMetadata.workdir,
			buildMetadata.envVars,
		)
	case "USER":
		// args: [username]
		if len(args) < 1 {
			return fmt.Errorf("USER requires a username argument")
		}

		return sandboxtools.RunCommand(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			"adduser "+args[0],
			"root",
			nil,
			buildMetadata.envVars,
		)
	case "WORKDIR":
		// args: [path]
		if len(args) < 1 {
			return fmt.Errorf("WORKDIR requires a path argument")
		}

		return sandboxtools.RunCommand(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`mkdir -p "%s"`, utils.Sprintp(buildMetadata.workdir)),
			buildMetadata.user,
			nil,
			buildMetadata.envVars,
		)
	default:
		return fmt.Errorf("unsupported command type: %s", cmdType)
	}
}
