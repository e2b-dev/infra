package paths

import (
	"path"
)

func buildStoragePath(cacheScope string, saveType string, file string) string {
	return path.Join(cacheScope, saveType, file)
}

func GetLayerFilesCachePath(cacheScope string, hash string) string {
	return buildStoragePath(cacheScope, "files", hash+".tar")
}

func HashToPath(cacheScope, hash string) string {
	return buildStoragePath(cacheScope, "index", hash)
}
