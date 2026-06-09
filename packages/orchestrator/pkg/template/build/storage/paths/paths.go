package paths

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
)

const sha256HexLength = 64

func validatePathSegment(name, value string) error {
	if value == "." || !fs.ValidPath(value) || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%s contains invalid path characters", name)
	}

	return nil
}

func validateSHA256Hex(name, value string) error {
	if len(value) != sha256HexLength {
		return fmt.Errorf("%s must be a SHA-256 hex hash", name)
	}

	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("%s must be a SHA-256 hex hash", name)
		}
	}

	return nil
}

func buildStoragePath(cacheScope string, saveType string, file string) (string, error) {
	if err := validatePathSegment("cache scope", cacheScope); err != nil {
		return "", err
	}

	if err := validatePathSegment("storage file", file); err != nil {
		return "", err
	}

	return path.Join(cacheScope, saveType, file), nil
}

func GetLayerFilesCachePath(cacheScope string, hash string) (string, error) {
	if err := validateSHA256Hex("files hash", hash); err != nil {
		return "", err
	}

	return buildStoragePath(cacheScope, "files", hash+".tar")
}

func HashToPath(cacheScope, hash string) (string, error) {
	if err := validateSHA256Hex("hash", hash); err != nil {
		return "", err
	}

	return buildStoragePath(cacheScope, "index", hash)
}
