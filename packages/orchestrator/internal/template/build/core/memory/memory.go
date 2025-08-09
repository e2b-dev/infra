package memory

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
)

const (
	memoryBuildFileName = "memfile.build"
)

func NewMemory(memoryBuildDir string, sizeMb int64) (string, error) {
	emptyMemoryFilePath := filepath.Join(memoryBuildDir, memoryBuildFileName)
	emptyMemoryFile, err := os.Create(emptyMemoryFilePath)
	if err != nil {
		return "", fmt.Errorf("creating blank memfile: %w", err)
	}
	defer emptyMemoryFile.Close()

	err = emptyMemoryFile.Truncate(sizeMb << constants.ToMBShift)
	if err != nil {
		return "", fmt.Errorf("truncating blank memfile: %w", err)
	}

	// Sync the metadata to disk.
	// This is important to ensure that the file is fully written when used by other processes, like FC.
	err = emptyMemoryFile.Sync()
	if err != nil {
		return "", fmt.Errorf("syncing blank memfile: %w", err)
	}

	return emptyMemoryFilePath, nil
}
