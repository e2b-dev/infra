package builder

import "fmt"

func GetLayerFilesCachePath(templateID string, hash string) string {
	return fmt.Sprintf("builder/cache/%s/%s.tar", templateID, hash)
}
