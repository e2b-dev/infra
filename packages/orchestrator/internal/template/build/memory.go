package build

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	memoryBuildFileName = "memfile.build"
)

func NewMemory(memoryBuildDir string, sizeMb int64) (string, error) {
	emptyMemoryFilePath := filepath.Join(memoryBuildDir, memoryBuildFileName)
	emptyMemoryFile, err := os.Create(emptyMemoryFilePath)
	if err != nil {
		return "", fmt.Errorf("error creating blank memfile: %w", err)
	}
	defer emptyMemoryFile.Close()
	err = emptyMemoryFile.Truncate(sizeMb << ToMBShift)
	if err != nil {
		return "", fmt.Errorf("error truncating blank memfile: %w", err)
	}

	err = emptyMemoryFile.Sync()
	if err != nil {
		return "", fmt.Errorf("error syncing blank memfile: %w", err)
	}

	return emptyMemoryFilePath, nil
}
