package envd

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

const testFolder = "/test"

func TestListDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	envdClient := setup.GetEnvdClient(t, ctx)

	createDir(t, sbx, testFolder)
	createDir(t, sbx, fmt.Sprintf("%s/test-dir", testFolder))
	createDir(t, sbx, fmt.Sprintf("%s/test-dir/sub-dir-1", testFolder))
	createDir(t, sbx, fmt.Sprintf("%s/test-dir/sub-dir-2", testFolder))

	filePath := fmt.Sprintf("%s/test-dir/sub-dir-1/file.txt", testFolder)
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	createFileResp, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.SandboxID, sbx.ClientID),
	)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, createFileResp.StatusCode())

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
			setup.SetSandboxHeader(req.Header(), sbx.SandboxID, sbx.ClientID)
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

func TestCreateFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	envdClient := setup.GetEnvdClient(t, ctx)
	filePath := "test.txt"
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	createFileResp, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.SandboxID, sbx.ClientID),
	)
	assert.NoError(t, err)

	assert.Equal(t, http.StatusOK, createFileResp.StatusCode())
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
			Args: []string{"-la", "/home/user"},
		},
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID, sbx.ClientID)
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

func TestStat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	envdClient := setup.GetEnvdClient(t, ctx)
	filePath := "/home/user/test.txt"
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	createFileResp, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.SandboxID, sbx.ClientID),
	)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, createFileResp.StatusCode())

	req := connect.NewRequest(&filesystem.StatRequest{
		Path: filePath,
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	statResp, err := envdClient.FilesystemClient.Stat(ctx, req)
	assert.NoError(t, err)

	// Verify the stat response
	assert.NotNil(t, statResp.Msg)
	assert.NotNil(t, statResp.Msg.Entry)
	entry := statResp.Msg.Entry

	// Verify basic file info
	assert.Equal(t, "test.txt", entry.Name)
	assert.Equal(t, filePath, entry.Path)
	assert.Equal(t, filesystem.FileType_FILE_TYPE_FILE, entry.Type)

	// Verify permissions and ownership
	assert.Equal(t, uint32(0644), entry.Mode)
	assert.Equal(t, "-rw-r--r--", entry.Permissions)
	assert.Equal(t, "user", entry.Owner)
	assert.Equal(t, "user", entry.Group)

	// Verify file size
	assert.Equal(t, int64(13), entry.Size)

	// Verify modified time
	assert.NotNil(t, entry.ModifiedTime)
}

func createTextFile(tb testing.TB, path string, content string) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", path)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = part.Write([]byte(content))
	if err != nil {
		tb.Fatal(err)
	}
	err = writer.Close()
	if err != nil {
		tb.Fatal(err)
	}

	return body, writer.FormDataContentType()
}

func createDir(tb testing.TB, sbx *api.Sandbox, path string) {
	tb.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setup.GetEnvdClient(tb, ctx)
	req := connect.NewRequest(&filesystem.MakeDirRequest{
		Path: path,
	})
	setup.SetSandboxHeader(req.Header(), sbx.SandboxID, sbx.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	_, err := client.FilesystemClient.MakeDir(ctx, req)
	if err != nil {
		tb.Fatal(err)
	}
}
