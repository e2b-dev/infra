package filesystem

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func (Service) ListDir(ctx context.Context, req *connect.Request[rpc.ListDirRequest]) (*connect.Response[rpc.ListDirResponse], error) {
	depth := req.Msg.GetDepth()
	if depth == 0 {
		depth = 1 // default depth to current directory
	}

	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return nil, err
	}

	requestedPath := req.Msg.GetPath()

	// Expand the path
	requestedPath, err = permissions.ExpandAndResolve(requestedPath, u)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	resolvedPath, err := followSymlink(requestedPath)
	if err != nil {
		return nil, err
	}

	err = checkIfDirectory(resolvedPath)
	if err != nil {
		return nil, err
	}

	entries, err := walkDir(requestedPath, resolvedPath, int(depth))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.ListDirResponse{
		Entries: entries,
	}), nil
}

func (Service) MakeDir(ctx context.Context, req *connect.Request[rpc.MakeDirRequest]) (*connect.Response[rpc.MakeDirResponse], error) {
	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return nil, err
	}

	dirPath, err := permissions.ExpandAndResolve(req.Msg.GetPath(), u)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	stat, err := os.Stat(dirPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error getting file info: %w", err))
	}

	if err == nil {
		if stat.IsDir() {
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("directory already exists: %s", dirPath))
		}

		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path already exists but it is not a directory: %s", dirPath))
	}

	uid, gid, userErr := permissions.GetUserIds(u)
	if userErr != nil {
		return nil, connect.NewError(connect.CodeInternal, userErr)
	}

	userErr = permissions.EnsureDirs(dirPath, int(uid), int(gid))
	if userErr != nil {
		return nil, connect.NewError(connect.CodeInternal, userErr)
	}

	return connect.NewResponse(&rpc.MakeDirResponse{
		Entry: &rpc.EntryInfo{
			Name: path.Base(dirPath),
			Type: rpc.FileType_FILE_TYPE_DIRECTORY,
			Path: dirPath,
		},
	}), nil
}

// followSymlink resolves a symbolic link to its target path.
func followSymlink(path string) (string, error) {
	// Resolve symlinks
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("path not found: %w", err))
		}

		if strings.Contains(err.Error(), "too many links") {
			return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("cyclic symlink or chain >255 links at %q", path))
		}

		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("error resolving symlink: %w", err))
	}

	return resolvedPath, nil
}

// checkIfDirectory checks if the given path is a directory.
func checkIfDirectory(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("directory not found: %w", err))
		}

		return connect.NewError(connect.CodeInternal, fmt.Errorf("error getting file info: %w", err))
	}

	if !stat.IsDir() {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path is not a directory: %s", path))
	}

	return nil
}

// walkDir walks the directory tree starting from dirPath up to the specified depth (doesn't follow symlinks).
func walkDir(requestedPath string, dirPath string, depth int) (entries []*rpc.EntryInfo, err error) {
	err = filepath.WalkDir(dirPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if path == dirPath {
			return nil
		}

		// Calculate current depth
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		currentDepth := len(strings.Split(relPath, string(os.PathSeparator)))

		if currentDepth > depth {
			return filepath.SkipDir
		}

		entries = append(entries, &rpc.EntryInfo{
			Name: entry.Name(),
			Type: getEntryType(entry),
			// Return the requested path as the base path instead of the symlink-resolved path
			Path: filepath.Join(requestedPath, relPath),
		})

		return nil
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error reading directory %s: %w", dirPath, err))
	}

	return entries, nil
}
