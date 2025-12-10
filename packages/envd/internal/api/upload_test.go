package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessFile(t *testing.T) {
	realUser := &user.User{
		Uid: strconv.Itoa(os.Getuid()),
		Gid: strconv.Itoa(os.Getgid()),
	}

	newRequest := func(content []byte) (*http.Request, io.Reader) {
		request := &http.Request{
			ContentLength: int64(len(content)),
		}
		buffer := bytes.NewBuffer(content)

		return request, buffer
	}

	var emptyReq http.Request
	var emptyPath string
	var emptyPart *bytes.Buffer
	var emptyLogger zerolog.Logger

	t.Run("failed to get user ids", func(t *testing.T) {
		invalidUser := user.User{Uid: "-12345", Gid: "-12345"}

		httpStatus, err := processFile(&emptyReq, emptyPath, emptyPart, &invalidUser, emptyLogger)
		require.Error(t, err)
		assert.Equal(t, http.StatusInternalServerError, httpStatus)
		assert.ErrorContains(t, err, "error getting user ids: ")
	})

	t.Run("failed to ensure directories", func(t *testing.T) {
		httpStatus, err := processFile(&emptyReq, "/proc/invalid/not-real", emptyPart, realUser, emptyLogger)
		require.Error(t, err)
		assert.Equal(t, http.StatusInternalServerError, httpStatus)
		assert.ErrorContains(t, err, "error ensuring directories: ")
	})

	t.Run("attempt to replace directory with a file", func(t *testing.T) {
		tempDir := t.TempDir()

		httpStatus, err := processFile(&emptyReq, tempDir, emptyPart, realUser, emptyLogger)
		require.Error(t, err)
		assert.Equal(t, http.StatusBadRequest, httpStatus, err.Error())
		assert.ErrorContains(t, err, "path is a directory: ")
	})

	t.Run("fail to create file", func(t *testing.T) {
		httpStatus, err := processFile(&emptyReq, "/proc/invalid-filename", emptyPart, realUser, emptyLogger)
		require.Error(t, err)
		assert.Equal(t, http.StatusInternalServerError, httpStatus)
		assert.ErrorContains(t, err, "error opening file: ")
	})

	t.Run("out of disk space", func(t *testing.T) {
		// make a tiny tmpfs mount
		mountSize := 1024
		tempDir := createTmpfsMount(t, mountSize)

		// create test file
		firstFileSize := mountSize / 2
		tempFile1 := filepath.Join(tempDir, "test-file-1")

		// fill it up
		cmd := exec.CommandContext(t.Context(),
			"dd", "if=/dev/zero", "of="+tempFile1, fmt.Sprintf("bs=%d", firstFileSize), "count=1")
		err := cmd.Run()
		require.NoError(t, err)

		// create a new file that would fill up the
		secondFileContents := make([]byte, mountSize*2)
		for index := range secondFileContents {
			secondFileContents[index] = 'a'
		}

		// try to replace it
		request, buffer := newRequest(secondFileContents)
		tempFile2 := filepath.Join(tempDir, "test-file-2")
		httpStatus, err := processFile(request, tempFile2, buffer, realUser, emptyLogger)
		require.Error(t, err)
		assert.Equal(t, http.StatusInsufficientStorage, httpStatus)
		assert.ErrorContains(t, err, "attempted to write 2048 bytes: not enough disk space")
	})

	t.Run("happy path", func(t *testing.T) {
		tempDir := t.TempDir()
		tempFile := filepath.Join(tempDir, "test-file")

		content := []byte("test-file-contents")
		request, buffer := newRequest(content)

		httpStatus, err := processFile(request, tempFile, buffer, realUser, emptyLogger)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, httpStatus)

		data, err := os.ReadFile(tempFile)
		require.NoError(t, err)
		assert.Equal(t, content, data)
	})

	t.Run("overwrite file on full disk", func(t *testing.T) {
		// make a tiny tmpfs mount
		sizeInBytes := 1024
		tempDir := createTmpfsMount(t, 1024)

		// create test file
		tempFile := filepath.Join(tempDir, "test-file")

		// fill it up
		cmd := exec.CommandContext(t.Context(), "dd", "if=/dev/zero", "of="+tempFile, fmt.Sprintf("bs=%d", sizeInBytes), "count=1")
		err := cmd.Run()
		require.NoError(t, err)

		// try to replace it
		content := []byte("test-file-contents")
		request, buffer := newRequest(content)
		httpStatus, err := processFile(request, tempFile, buffer, realUser, emptyLogger)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, httpStatus)
	})

	t.Run("update sysfs or other virtual fs", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("skipping sysfs updates: Operation not permitted with non-root user")
		}

		filePath := "/sys/fs/cgroup/user.slice/cpu.weight"
		newContent := []byte("102\n")
		request, buffer := newRequest(newContent)

		httpStatus, err := processFile(request, filePath, buffer, realUser, emptyLogger)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, httpStatus)

		data, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, newContent, data)
	})

	t.Run("replace file", func(t *testing.T) {
		tempDir := t.TempDir()
		tempFile := filepath.Join(tempDir, "test-file")

		err := os.WriteFile(tempFile, []byte("old-contents"), 0o644)
		require.NoError(t, err)

		newContent := []byte("new-file-contents")
		request, buffer := newRequest(newContent)

		httpStatus, err := processFile(request, tempFile, buffer, realUser, emptyLogger)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, httpStatus)

		data, err := os.ReadFile(tempFile)
		require.NoError(t, err)
		assert.Equal(t, newContent, data)
	})
}

func createTmpfsMount(t *testing.T, sizeInBytes int) string {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("skipping sysfs updates: Operation not permitted with non-root user")
	}

	tempDir := t.TempDir()

	cmd := exec.CommandContext(t.Context(), "mount", "-t", "tmpfs", "tmpfs", tempDir, "-o", "size="+strconv.Itoa(sizeInBytes))
	err := cmd.Run()
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		cmd := exec.CommandContext(ctx, "umount", tempDir)
		err := cmd.Run()
		require.NoError(t, err)
	})

	return tempDir
}
