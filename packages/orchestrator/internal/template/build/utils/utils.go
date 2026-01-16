package utils

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
)

func GetFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("error opening file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("failed to close file: %v", err)
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("error calculating file hash: %w", err)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}
