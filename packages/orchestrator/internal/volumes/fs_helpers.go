package volumes

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensureParentDirs creates all missing parent directories for dirPath (not including dirPath itself)
// with the provided mode, then applies chmod with the same mode to only those directories that were
// created during this call (to counteract process umask). Traversal is bounded by volRoot.
func ensureParentDirs(volRoot, dirPath string, mode os.FileMode) error {
	if dirPath == "" {
		return nil
	}

	volRoot = filepath.Clean(volRoot)
	dirPath = filepath.Clean(dirPath)

	// Determine which parent directories do not exist yet, up to the volume root.
	var toChmod []string
	cur := dirPath
	for cur != volRoot {
		if fi, err := os.Stat(cur); err == nil {
			if fi.IsDir() {
				break // first existing directory reached
			}
			// exists but not a directory – let MkdirAll surface an error later
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat parent directory %q: %w", cur, err)
		}

		toChmod = append(toChmod, cur)

		next := filepath.Clean(filepath.Dir(cur))
		if next == cur { // reached filesystem root just in case
			break
		}
		cur = next
	}

	if err := os.MkdirAll(dirPath, mode); err != nil {
		return fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Only chmod the directories that were created by this call (precomputed above).
	// Iterate from highest parent to deepest child for determinism.
	for i := len(toChmod) - 1; i >= 0; i-- {
		p := toChmod[i]
		if err := os.Chmod(p, mode); err != nil {
			if os.IsNotExist(err) {
				// Race or unexpected removal; treat as an error to be explicit.
				return fmt.Errorf("failed to chmod created parent directory %q: %w", p, err)
			}

			return fmt.Errorf("failed to set mode for created parent directory %q: %w", p, err)
		}
	}

	return nil
}
