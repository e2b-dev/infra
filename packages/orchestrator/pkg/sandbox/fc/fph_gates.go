package fc

import (
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// MinFreePageHintingKernelVersion is the minimum guest kernel version that
// contains the virtio-balloon free-page-hinting race fix. Templates built
// against an older kernel get the balloon installed with FreePageHinting
// disabled so the race can't be triggered, regardless of any runtime
// LaunchDarkly toggle. Bump this once the fixed kernel is published to
// e2b-dev/fc-kernels.
const MinFreePageHintingKernelVersion = "999.0.0"

// kernelSupportsFreePageHinting reports whether kernelVersion (e.g.
// "vmlinux-6.1.158") includes the FPH/MADV_DONTNEED race fix.
func kernelSupportsFreePageHinting(kernelVersion string) bool {
	v := strings.TrimPrefix(kernelVersion, "vmlinux-")
	ok, _ := utils.IsGTEVersion(v, MinFreePageHintingKernelVersion)

	return ok
}

// fcSupportsFreePageHinting reports whether the Firecracker version exposes
// the start_balloon_hinting / describe_balloon_hinting API (v1.14+).
func fcSupportsFreePageHinting(fcVersion string) bool {
	info, err := fcversion.New(fcVersion)
	if err != nil {
		return false
	}

	return info.HasFreePageHinting()
}
