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

// FCSupportsMemfd reports whether the FC build accepts the use_memfd field
// on snapshot load. Combined with UseMemFdFlag in sandbox.go so an old FC
// won't reject the request even if the flag is on.
func FCSupportsMemfd(fcVersion string) bool {
	info, err := fcversion.New(fcVersion)
	if err != nil {
		return false
	}

	return info.HasMemfd()
}
