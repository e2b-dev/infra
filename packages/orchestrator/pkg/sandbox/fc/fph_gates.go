package fc

import (
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// MinFreePageHintingKernelVersion is the minimum guest kernel version that
// contains the FPH/MADV_DONTNEED race fix. Bump once the fixed kernel ships.
const MinFreePageHintingKernelVersion = "999.0.0"

func kernelSupportsFreePageHinting(kernelVersion string) bool {
	v := strings.TrimPrefix(kernelVersion, "vmlinux-")
	ok, _ := utils.IsGTEVersion(v, MinFreePageHintingKernelVersion)

	return ok
}

func fcSupportsFreePageHinting(fcVersion string) bool {
	info, err := fcversion.New(fcVersion)
	if err != nil {
		return false
	}

	return info.HasFreePageHinting()
}
