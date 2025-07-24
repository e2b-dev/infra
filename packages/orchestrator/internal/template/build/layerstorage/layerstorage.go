package layerstorage

import (
	"path"
)

func buildStoragePath(templateID string, saveType string, file string) string {
	return path.Join(templateID, saveType, file)
}

func GetLayerFilesCachePath(templateID string, hash string) string {
	return buildStoragePath(templateID, "files", hash+".tar")
}

func HashToPath(templateID, hash string) string {
	return buildStoragePath(templateID, "index", hash)
}
