package fcversion

func (v *Info) HasHugePages() bool {
	if v.lastReleaseVersion.Major() > 1 || (v.lastReleaseVersion.Major() == 1 && v.lastReleaseVersion.Minor() >= 7) {
		return true
	}

	return false
}

func (v *Info) HasFreePageReporting() bool {
	return v.lastReleaseVersion.Major() > 1 || (v.lastReleaseVersion.Major() == 1 && v.lastReleaseVersion.Minor() >= 14)
}

// HasFreePageHinting reports whether the Firecracker version exposes the
// balloon free-page-hinting API (start_balloon_hinting / describe_balloon_hinting).
// Introduced in v1.14.
func (v *Info) HasFreePageHinting() bool {
	return v.lastReleaseVersion.Major() > 1 || (v.lastReleaseVersion.Major() == 1 && v.lastReleaseVersion.Minor() >= 14)
}
