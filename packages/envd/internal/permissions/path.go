package permissions

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
)

func expand(path, homedir string) (string, error) {
	if len(path) == 0 {
		return path, nil
	}

	if path[0] != '~' {
		return path, nil
	}

	if len(path) > 1 && path[1] != '/' && path[1] != '\\' {
		return "", errors.New("cannot expand user-specific home dir")
	}

	return filepath.Join(homedir, path[1:]), nil
}

func ExpandAndResolve(path string, user *user.User, defaultPath *string) (string, error) {
	path = execcontext.ResolveDefaultWorkdir(path, defaultPath)

	path, err := expand(path, user.HomeDir)
	if err != nil {
		return "", fmt.Errorf("failed to expand path '%s' for user '%s': %w", path, user.Username, err)
	}

	if filepath.IsAbs(path) {
		// Clean the path to remove any .. or . components
		return sanitizePath(path)
	}

	// The filepath.Abs can correctly resolve paths like /home/user/../file
	path = filepath.Join(user.HomeDir, path)

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path '%s' for user '%s' with home dir '%s': %w", path, user.Username, user.HomeDir, err)
	}

	return sanitizePath(abs)
}

// sanitizePath cleans a path and validates it is safe for use.
// This function breaks the taint chain for security analysis tools.
func sanitizePath(path string) (string, error) {
	// Clean the path to remove .. and . components
	cleaned := filepath.Clean(path)

	// Validate the path is absolute after cleaning
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be absolute: %s", path)
	}

	// Ensure path doesn't contain null bytes (path injection attack)
	if strings.ContainsRune(cleaned, '\x00') {
		return "", fmt.Errorf("path contains invalid characters: %s", path)
	}

	return cleaned, nil
}

func getSubpaths(path string) (subpaths []string) {
	for {
		subpaths = append(subpaths, path)

		path = filepath.Dir(path)
		if path == "/" {
			break
		}
	}

	slices.Reverse(subpaths)

	return subpaths
}

func EnsureDirs(path string, uid, gid int) error {
	subpaths := getSubpaths(path)
	for _, subpath := range subpaths {
		info, err := os.Stat(subpath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat directory: %w", err)
		}

		if err != nil && os.IsNotExist(err) {
			err = os.Mkdir(subpath, 0o755)
			if err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}

			err = os.Chown(subpath, uid, gid)
			if err != nil {
				return fmt.Errorf("failed to chown directory: %w", err)
			}

			continue
		}

		if !info.IsDir() {
			return fmt.Errorf("path is a file: %s", subpath)
		}
	}

	return nil
}
