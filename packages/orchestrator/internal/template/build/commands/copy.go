package commands

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	txtTemplate "text/template"

	"github.com/bmatcuk/doublestar/v4"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Copy struct {
	FilesStorage storage.StorageProvider
	CacheScope   string
}

var _ Command = (*Copy)(nil)

type copyScriptData struct {
	SourcePath  string
	TargetPath  string
	Owner       string
	Permissions string

	// Workdir is the working directory for the target path resolution if relative.
	Workdir string
	// User is used for filling the workdir if empty.
	User string
}

//go:embed copy_script.sh
var copyScriptFile string
var copyScriptTemplate = txtTemplate.Must(txtTemplate.New("copy-script-template").Parse(copyScriptFile))

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
	logger logger.Logger,
	_ zapcore.Level,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	_ string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	cmdType := strings.ToUpper(step.GetType())
	args, err := parseCopyArgs(step.GetArgs(), cmdMetadata.User)
	if err != nil {
		return metadata.Context{}, err
	}

	if step.FilesHash == nil || step.GetFilesHash() == "" {
		return metadata.Context{}, fmt.Errorf("%s requires files hash to be set", cmdType)
	}

	// 1) Download the layer tar file from the storage to the local filesystem
	obj, err := c.FilesStorage.OpenBlob(ctx, paths.GetLayerFilesCachePath(c.CacheScope, step.GetFilesHash()), storage.BuildLayerFileObjectType)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to open files object from storage: %w", err)
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
		return metadata.Context{}, fmt.Errorf("failed to create temporary file for layer tar: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, pr)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to copy layer tar data to temporary file: %w", err)
	}

	// The file is automatically cleaned up by the sandbox restart in the last step.
	// This is happening because the /tmp is mounted as a tmpfs and deleted on restart.
	sbxTargetPath := filepath.Join("/tmp", fmt.Sprintf("%s.tar", step.GetFilesHash()))
	// 2) Copy the tar file to the sandbox
	err = sandboxtools.CopyFile(ctx, proxy, sandboxID, "root", tmpFile.Name(), sbxTargetPath)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to copy layer tar data to sandbox: %w", err)
	}

	// Create nested unpack directory to allow multiple files in the root be correctly detected
	sbxUnpackPath := filepath.Join("/tmp", step.GetFilesHash(), "unpack")

	// 3) Extract the tar file in the sandbox's /tmp directory
	err = sandboxtools.RunCommand(
		ctx,
		proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "%s" && tar -xzvf "%s" -C "%s"`, sbxUnpackPath, sbxTargetPath, sbxUnpackPath),
		cmdMetadata.WithUser("root"),
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to extract files: %w", err)
	}

	var moveScript bytes.Buffer
	err = copyScriptTemplate.Execute(&moveScript, copyScriptData{
		Workdir: utils.DerefOrDefault(cmdMetadata.WorkDir, ""),
		User:    cmdMetadata.User,

		SourcePath: filepath.Join(sbxUnpackPath, args.SourcePath),
		TargetPath: args.TargetPath,
		Owner:      args.Owner,
		// Optional permissions
		Permissions: args.Permissions,
	})
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to execute copy script template: %w", err)
	}

	// Run the move script as root so it can chown files to any user
	// The script handles both ownership and permissions on the source before moving
	err = sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		zapcore.DebugLevel,
		"unpack",
		sandboxID,
		moveScript.String(),
		cmdMetadata.WithUser("root"),
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to move files in sandbox: %w", err)
	}

	return cmdMetadata, nil
}

func ensureTrailingSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}

	return s + "/"
}

type copyArgs struct {
	SourcePath  string
	TargetPath  string
	Owner       string
	Permissions string
}

func parseCopyArgs(args []string, defaultUser string) (*copyArgs, error) {
	// Validate minimum arguments
	// args: [localPath containerPath optional_owner optional_permissions]
	if len(args) < 2 {
		return nil, errors.New("COPY requires a local path and a container path argument")
	}

	// Remove all glob patterns, they are handled on the client side already
	// Add / always at the end to ensure the last file/directory is also included if it doesn't contain a glob pattern
	sourcePath, _ := doublestar.SplitPattern(ensureTrailingSlash(args[0]))

	// Parse target path
	targetPath := args[1]

	// Determine owner (default to defaultUser:defaultUser)
	owner := fmt.Sprintf("%s:%s", defaultUser, defaultUser)
	if len(args) >= 3 && args[2] != "" {
		owner = args[2]
		// If no group specified, use the same as user
		if !strings.Contains(owner, ":") {
			owner = fmt.Sprintf("%s:%s", owner, owner)
		}
	}

	// Parse optional permissions
	permissions := ""
	if len(args) >= 4 {
		permissions = args[3]
	}

	return &copyArgs{
		SourcePath:  sourcePath,
		TargetPath:  targetPath,
		Owner:       owner,
		Permissions: permissions,
	}, nil
}
