package builder

import "fmt"

func GetLayerFilesCachePath(templateID string, hash string) string {
	return fmt.Sprintf("builder/%s/files/%s.tar", templateID, hash)
}
