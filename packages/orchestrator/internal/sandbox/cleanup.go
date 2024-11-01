package sandbox

import (
	"errors"
	"fmt"
	"os"

	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// HandleCleanup calls the cleanup functions in reverse order and returns the the joined error.
func HandleCleanup(cleanup []func() error) error {
	errs := make([]error, len(cleanup))

	for i := len(cleanup) - 1; i >= 0; i-- {
		cleanupFn := cleanup[i]
		errs[i] = cleanupFn()
	}

	return errors.Join(errs...)
}

func cleanupFiles(files *templateStorage.SandboxFiles) error {
	var errs []error

	for _, file := range []string{
		files.SandboxCacheDir(),
		files.SandboxFirecrackerSocketPath(),
		files.SandboxUffdSocketPath(),
	} {
		err := os.RemoveAll(file)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to delete %s: %w", file, err))
		}
	}

	return errors.Join(errs...)
}
