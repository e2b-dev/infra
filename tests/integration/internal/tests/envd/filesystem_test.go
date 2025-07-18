package envd

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/filesystem"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

const (
	userHome   = "/home/user"
	testFolder = "/test"
)

func TestListDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	envdClient := setup.GetEnvdClient(t, ctx)

	utils.CreateDir(t, sbx, testFolder)
	utils.CreateDir(t, sbx, fmt.Sprintf("%s/test-dir", testFolder))
	utils.CreateDir(t, sbx, fmt.Sprintf("%s/test-dir/sub-dir-1", testFolder))
	utils.CreateDir(t, sbx, fmt.Sprintf("%s/test-dir/sub-dir-2", testFolder))

	filePath := fmt.Sprintf("%s/test-dir/sub-dir-1/file.txt", testFolder)
	utils.UploadFile(t, ctx, sbx, envdClient, filePath, []byte("Hello, World!"))

	tests := []struct {
		name          string
		depth         uint32
		expectedPaths []string
	}{
		{
			name:  "depth 0 lists only root directory",
			depth: 0,
			expectedPaths: []string{
				fmt.Sprintf("%s/test-dir", testFolder),
			},
		},
		{
			name:  "depth 1 lists root directory",
			depth: 1,
			expectedPaths: []string{
				fmt.Sprintf("%s/test-dir", testFolder),
			},
		},
		{
			name:  "depth 2 lists first level of subdirectories (in this case the root directory)",
			depth: 2,
			expectedPaths: []string{
				fmt.Sprintf("%s/test-dir", testFolder),
				fmt.Sprintf("%s/test-dir/sub-dir-1", testFolder),
				fmt.Sprintf("%s/test-dir/sub-dir-2", testFolder),
			},
		},
		{
			name:  "depth 3 lists all directories and files",
			depth: 3,
			expectedPaths: []string{
				fmt.Sprintf("%s/test-dir", testFolder),
				fmt.Sprintf("%s/test-dir/sub-dir-1", testFolder),
				fmt.Sprintf("%s/test-dir/sub-dir-2", testFolder),
				fmt.Sprintf("%s/test-dir/sub-dir-1/file.txt", testFolder),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := connect.NewRequest(&filesystem.ListDirRequest{
				Path:  testFolder,
				Depth: tt.depth,
			})
			setup.SetSandboxHeader(req.Header(), sbx.SandboxID)
			setup.SetUserHeader(req.Header(), "user")
			folderListResp, err := envdClient.FilesystemClient.ListDir(ctx, req)
			assert.NoError(t, err)

			assert.NotEmpty(t, folderListResp.Msg)
			assert.Equal(t, len(tt.expectedPaths), len(folderListResp.Msg.Entries))

			actualPaths := make([]string, len(folderListResp.Msg.Entries))
			for i, entry := range folderListResp.Msg.Entries {
				actualPaths[i] = entry.Path
			}
			assert.ElementsMatch(t, tt.expectedPaths, actualPaths)
		})
	}
}

func TestFilePermissions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	envdClient := setup.GetEnvdClient(t, ctx)
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "ls",
			Args: []string{"-la", userHome},
		},
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	stream, err := envdClient.ProcessClient.Start(
		ctx,
		req,
	)

	assert.NoError(t, err)

	defer stream.Close()

	out := []string{}

	for stream.Receive() {
		msg := stream.Msg()
		out = append(out, msg.String())
	}

	// in the output, we should see the files .bashrc and .profile, and they should have the correct permissions
	for _, line := range out {
		if strings.Contains(line, ".bashrc") || strings.Contains(line, ".profile") {
			assert.Contains(t, line, "-rw-r--r--")
			assert.Contains(t, line, "user user")
		}
	}
}

func TestRelativePath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	envdClient := setup.GetEnvdClient(t, ctx)

	utils.CreateDir(t, sbx, path.Join(userHome, testFolder))
	utils.UploadFile(t, ctx, sbx, envdClient, path.Join(userHome, testFolder, "test.txt"), []byte("Hello, World!"))

	req := connect.NewRequest(&filesystem.ListDirRequest{
		Path:  testFolder,
		Depth: 0,
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	folderListResp, err := envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NoError(t, err)

	require.NotEmpty(t, folderListResp.Msg)
	assert.Len(t, folderListResp.Msg.Entries, 1)

	assert.Equal(t, path.Join(userHome, testFolder, "test.txt"), folderListResp.Msg.Entries[0].Path)
}
