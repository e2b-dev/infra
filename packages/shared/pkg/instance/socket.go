package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	socketReadyCheckInterval = 100 * time.Millisecond
	SocketWaitTimeout        = 2 * time.Second
)

func GetSocketPath(instanceID string) (string, error) {
	filename := strings.Join([]string{
		"firecracker-",
		instanceID,
		".socket",
	}, "")

	var dir string

	if checkExistsAndDir(os.TempDir()) {
		dir = os.TempDir()
	} else {
		errMsg := fmt.Errorf("unable to find a location for firecracker socket")
		return "", errMsg
	}

	return filepath.Join(dir, filename), nil
}

func checkExistsAndDir(path string) bool {
	if path == "" {
		return false
	}

	if info, err := os.Stat(path); err == nil {
		return info.IsDir()
	}

	return false
}

func WaitForSocket(socketPath string, timeout time.Duration) error {
	start := time.Now()

	for {
		_, err := os.Stat(socketPath)
		if err == nil {
			// Socket file exists
			return nil
		} else if os.IsNotExist(err) {
			// Socket file doesn't exist yet

			// Check if timeout has been reached
			elapsed := time.Since(start)
			if elapsed >= timeout {
				return fmt.Errorf("timeout reached while waiting for socket file")
			}

			// Wait for a short duration before checking again
			time.Sleep(socketReadyCheckInterval)
		} else {
			// Error occurred while checking for socket file
			return err
		}
	}
}
