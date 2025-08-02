package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	txtTemplate "text/template"

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
	CacheScope   string
}

type copyScriptData struct {
	SourcePath string
	TargetPath string
}

var copyScriptTemplate = txtTemplate.Must(txtTemplate.New("copy-script-template").Parse(`
#!/bin/bash

# Get the parent folder of the source file/folder
sourceFolder="$(dirname "{{ .SourcePath}}")"

# Set targetPath relative to current working directory if not absolute
inputPath="{{ .TargetPath }}"
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
`))

// Execute implements the Copy command.
// It works in the following steps:
// 1) Downloads the layer tar file from the storage to the local filesystem
// 2) Copies the file to the sandbox's /tmp directory
// 3) Extracts it (still in the /tmp directory)
// 4) Moves the extracted files to the target path in the sandbox
//   - If the source is a file, it creates the parent directories and moves the file
//   - If the source is a directory, it moves all its contents to the target directory

// Note: The temporary files in the /tmp directory are cleaned up automatically on sandbox restart
// because the /tmp is mounted as a tmpfs and deleted on restart.
func (c *Copy) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata sandboxtools.CommandMetadata,
) (sandboxtools.CommandMetadata, error) {
	cmdType := strings.ToUpper(step.Type)
	args := step.Args
	// args: [localPath containerPath optional_owner optional_permissions]
	if len(args) < 2 {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("%s requires a local path and a container path argument", cmdType)
	}

	if step.FilesHash == nil || *step.FilesHash == "" {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("%s requires files hash to be set", cmdType)
	}

	// 1) Download the layer tar file from the storage to the local filesystem
	obj, err := c.FilesStorage.OpenObject(ctx, layerstorage.GetLayerFilesCachePath(c.CacheScope, *step.FilesHash))
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to open files object from storage: %w", err)
	}

	pr, pw := io.Pipe()
	// Start writing tar data to the pipe writer in a goroutine
	go func() {
		defer pw.Close()
		if _, err := obj.WriteTo(ctx, pw); err != nil {
			pw.CloseWithError(err)
		}
	}()

	tmpFile, err := os.CreateTemp("", "layer-file-*.tar")
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to create temporary file for layer tar: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, pr)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to copy layer tar data to temporary file: %w", err)
	}

	// The file is automatically cleaned up by the sandbox restart in the last step.
	// This is happening because the /tmp is mounted as a tmpfs and deleted on restart.
	sbxTargetPath := filepath.Join("/tmp", fmt.Sprintf("%s.tar", *step.FilesHash))
	// 2) Copy the tar file to the sandbox
	err = sandboxtools.CopyFile(ctx, tracer, proxy, sandboxID, cmdMetadata.User, tmpFile.Name(), sbxTargetPath)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to copy layer tar data to sandbox: %w", err)
	}

	sbxUnpackPath := filepath.Join("/tmp", *step.FilesHash)

	// 3) Extract the tar file in the sandbox's /tmp directory
	err = sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "%s" && tar -xzvf "%s" -C "%s"`, sbxUnpackPath, sbxTargetPath, sbxUnpackPath),
		cmdMetadata,
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to extract files in sandbox: %w", err)
	}

	// 4) Move the extracted files to the target path in the sandbox
	targetPath := args[1]
	var moveScript bytes.Buffer
	err = copyScriptTemplate.Execute(&moveScript, copyScriptData{
		SourcePath: filepath.Join(sbxUnpackPath, args[0]),
		TargetPath: targetPath,
	})
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to execute copy script template: %w", err)
	}

	err = sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		moveScript.String(),
		cmdMetadata,
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to move files in sandbox: %w", err)
	}

	// If optional owner is provided, set them
	if len(args) >= 3 {
		// Assumes the format of chown
		owner := args[2]
		if owner != "" {
			err = sandboxtools.RunCommand(
				ctx,
				tracer,
				proxy,
				sandboxID,
				fmt.Sprintf(`chown -R %s "%s"`, owner, targetPath),
				cmdMetadata,
			)
			if err != nil {
				return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to set ownership and permissions in sandbox: %w", err)
			}
		}
	}

	// If optional permissions are provided, set them
	if len(args) >= 4 {
		// This assumes the format of chmod
		permissions := args[3]

		if permissions != "" {
			err = sandboxtools.RunCommand(
				ctx,
				tracer,
				proxy,
				sandboxID,
				fmt.Sprintf(`chmod -R %s "%s"`, permissions, targetPath),
				cmdMetadata,
			)
			if err != nil {
				return sandboxtools.CommandMetadata{}, fmt.Errorf("failed to set ownership and permissions in sandbox: %w", err)
			}
		}
	}

	return cmdMetadata, nil
}
