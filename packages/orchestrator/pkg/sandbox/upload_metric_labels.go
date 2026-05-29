package sandbox

import "github.com/e2b-dev/infra/packages/shared/pkg/storage"

func uploadMetricFileType(fileType string) string {
	if fileType == storage.RootfsName {
		return "rootfs"
	}

	return fileType
}
