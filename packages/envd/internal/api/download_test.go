package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func TestGetFilesContentDisposition(t *testing.T) {
	t.Parallel()

	currentUser, err := user.Current()
	require.NoError(t, err)

	tests := []struct {
		name           string
		filename       string
		expectedHeader string
	}{
		{
			name:           "simple filename",
			filename:       "test.txt",
			expectedHeader: `inline; filename=test.txt`,
		},
		{
			name:           "filename with extension",
			filename:       "presentation.pptx",
			expectedHeader: `inline; filename=presentation.pptx`,
		},
		{
			name:           "filename with multiple dots",
			filename:       "archive.tar.gz",
			expectedHeader: `inline; filename=archive.tar.gz`,
		},
		{
			name:           "filename with spaces",
			filename:       "my document.pdf",
			expectedHeader: `inline; filename="my document.pdf"`,
		},
		{
			name:           "filename with quotes",
			filename:       `file"name.txt`,
			expectedHeader: `inline; filename="file\"name.txt"`,
		},
		{
			name:           "filename with backslash",
			filename:       `file\name.txt`,
			expectedHeader: `inline; filename="file\\name.txt"`,
		},
		{
			name:           "unicode filename",
			filename:       "\u6587\u6863.pdf", // 文档.pdf in Chinese
			expectedHeader: "inline; filename*=utf-8''%E6%96%87%E6%A1%A3.pdf",
		},
		{
			name:           "dotfile preserved",
			filename:       ".env",
			expectedHeader: `inline; filename=.env`,
		},
		{
			name:           "dotfile with extension preserved",
			filename:       ".gitignore",
			expectedHeader: `inline; filename=.gitignore`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a temp directory and file
			tempDir := t.TempDir()
			tempFile := filepath.Join(tempDir, tt.filename)
			err := os.WriteFile(tempFile, []byte("test content"), 0o644)
			require.NoError(t, err)

			// Create test API
			logger := zerolog.Nop()
			defaults := &execcontext.Defaults{
				EnvVars: utils.NewMap[string, string](),
				User:    currentUser.Username,
			}
			api := New(&logger, defaults, nil, false)

			// Create request and response recorder
			req := httptest.NewRequest(http.MethodGet, "/files?path="+url.QueryEscape(tempFile), nil)
			w := httptest.NewRecorder()

			// Call the handler
			params := GetFilesParams{
				Path:     &tempFile,
				Username: &currentUser.Username,
			}
			api.GetFiles(w, req, params)

			// Check response
			resp := w.Result()
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			// Verify Content-Disposition header
			contentDisposition := resp.Header.Get("Content-Disposition")
			assert.Equal(t, tt.expectedHeader, contentDisposition, "Content-Disposition header should be set with correct filename")
		})
	}
}

func TestGetFilesContentDispositionWithNestedPath(t *testing.T) {
	t.Parallel()

	currentUser, err := user.Current()
	require.NoError(t, err)

	// Create a temp directory with nested structure
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "subdir", "another")
	err = os.MkdirAll(nestedDir, 0o755)
	require.NoError(t, err)

	filename := "document.pdf"
	tempFile := filepath.Join(nestedDir, filename)
	err = os.WriteFile(tempFile, []byte("test content"), 0o644)
	require.NoError(t, err)

	// Create test API
	logger := zerolog.Nop()
	defaults := &execcontext.Defaults{
		EnvVars: utils.NewMap[string, string](),
		User:    currentUser.Username,
	}
	api := New(&logger, defaults, nil, false)

	// Create request and response recorder
	req := httptest.NewRequest(http.MethodGet, "/files?path="+url.QueryEscape(tempFile), nil)
	w := httptest.NewRecorder()

	// Call the handler
	params := GetFilesParams{
		Path:     &tempFile,
		Username: &currentUser.Username,
	}
	api.GetFiles(w, req, params)

	// Check response
	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify Content-Disposition header uses only the base filename, not the full path
	contentDisposition := resp.Header.Get("Content-Disposition")
	assert.Equal(t, `inline; filename=document.pdf`, contentDisposition, "Content-Disposition should contain only the filename, not the path")
}
