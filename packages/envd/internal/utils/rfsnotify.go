package utils

import "path/filepath"

// FsnotifyPath creates an optionally recursive path for fsnotify/fsnotify internal implementation
func FsnotifyPath(path string, recursive bool) string {
	if recursive {
		return filepath.Join(path, "...")
	}

	return path
}
