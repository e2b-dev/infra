package fcversion

func (v *Info) HasHugePages() bool {
	if v.lastReleaseVersion.Major() > 1 || (v.lastReleaseVersion.Major() == 1 && v.lastReleaseVersion.Minor() >= 7) {
		return true
	}

	return false
}
