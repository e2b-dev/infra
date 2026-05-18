package fc

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
)

// FCSupportsFreePageHinting reports whether the FC version exposes
// virtio-balloon free-page-hinting. Kernel-side eligibility is gated separately
// via LaunchDarkly.
func FCSupportsFreePageHinting(fcVersion string) bool {
	info, err := fcversion.New(fcVersion)
	if err != nil {
		return false
	}

	return info.HasFreePageHinting()
}
