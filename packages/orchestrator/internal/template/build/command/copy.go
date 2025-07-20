package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layerstorage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Copy struct {
	FilesStorage storage.StorageProvider
}

func (c *Copy) Execute(
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
	cmdType := strings.ToUpper(step.Type)
	args := step.Args
	// args: [localPath containerPath]
	if len(args) < 2 {
		return fmt.Errorf("%s requires a local path and a container path argument", cmdType)
	}

	if step.FilesHash == nil || *step.FilesHash == "" {
		return fmt.Errorf("%s requires files hash to be set", cmdType)
	}

	obj, err := c.FilesStorage.OpenObject(ctx, layerstorage.GetLayerFilesCachePath(templateID, *step.FilesHash))
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
	err = sandboxtools.CopyFile(ctx, tracer, proxy, sandboxID, cmdMetadata.User, tmpFile.Name(), sbxTargetPath)
	if err != nil {
		return fmt.Errorf("failed to copy layer tar data to sandbox: %w", err)
	}

	sbxUnpackPath := filepath.Join("/tmp", *step.FilesHash)

	err = sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
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

	err = sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		moveScript,
		cmdMetadata,
	)
	if err != nil {
		return fmt.Errorf("failed to move files in sandbox: %w", err)
	}

	return nil
}
