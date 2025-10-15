package filesystem

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func TestStat(t *testing.T) {
	t.Parallel()

	// Setup temp root and user
	root := t.TempDir()
	// Get the actual path to the temp directory (symlinks can cause issues)
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	u, err := user.Current()
	require.NoError(t, err)

	group, err := user.LookupGroupId(u.Gid)
	require.NoError(t, err)

	// Setup directory structure
	testFolder := filepath.Join(root, "test")
	err = os.MkdirAll(testFolder, 0o755)
	require.NoError(t, err)

	testFile := filepath.Join(testFolder, "file.txt")
	err = os.WriteFile(testFile, []byte("Hello, World!"), 0o644)
	require.NoError(t, err)

	linkedFile := filepath.Join(testFolder, "linked-file.txt")
	err = os.Symlink(testFile, linkedFile)
	require.NoError(t, err)

	// Service instance
	svc := mockService()

	// Helper to inject user into context
	injectUser := func(ctx context.Context, u *user.User) context.Context {
		return authn.SetInfo(ctx, u)
	}

	tests := []struct {
		name string
		path string
	}{
		{
			name: "Stat file directory",
			path: testFile,
		},
		{
			name: "Stat symlink to file",
			path: linkedFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := injectUser(t.Context(), u)
			req := connect.NewRequest(&filesystem.StatRequest{
				Path: tt.path,
			})
			resp, err := svc.Stat(ctx, req)
			require.NoError(t, err)
			require.NotEmpty(t, resp.Msg)
			require.NotNil(t, resp.Msg.GetEntry())
			assert.Equal(t, tt.path, resp.Msg.GetEntry().GetPath())
			assert.Equal(t, filesystem.FileType_FILE_TYPE_FILE, resp.Msg.GetEntry().GetType())
			assert.Equal(t, u.Username, resp.Msg.GetEntry().GetOwner())
			assert.Equal(t, group.Name, resp.Msg.GetEntry().GetGroup())
			assert.Equal(t, uint32(0o644), resp.Msg.GetEntry().GetMode())
			if tt.path == linkedFile {
				require.NotNil(t, resp.Msg.GetEntry().GetSymlinkTarget())
				assert.Equal(t, testFile, resp.Msg.GetEntry().GetSymlinkTarget())
			} else {
				assert.Empty(t, resp.Msg.GetEntry().GetSymlinkTarget())
			}
		})
	}
}
