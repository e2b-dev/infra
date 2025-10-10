package team

import (
	"github.com/e2b-dev/infra/packages/db/queries"
)

// TierWithAddons combines tier limits with active addons
type TierWithAddons struct {
	Tier   *queries.Tier
	Addons []queries.Addon
}

// ConcurrentInstances returns the total concurrent instances limit (tier + all active addons)
func (t *TierWithAddons) ConcurrentInstances() int64 {
	total := t.Tier.ConcurrentInstances
	for _, addon := range t.Addons {
		total += addon.ConcurrentInstances
	}
	return total
}

// ConcurrentTemplateBuilds returns the total concurrent template builds limit (tier + all active addons)
func (t *TierWithAddons) ConcurrentTemplateBuilds() int64 {
	total := t.Tier.ConcurrentTemplateBuilds
	for _, addon := range t.Addons {
		total += addon.ConcurrentTemplateBuilds
	}
	return total
}

// MaxVcpu returns the maximum vCPU limit (max of tier and all active addons)
func (t *TierWithAddons) MaxVcpu() int64 {
	max := t.Tier.MaxVcpu
	for _, addon := range t.Addons {
		if addon.MaxVcpu > max {
			max = addon.MaxVcpu
		}
	}
	return max
}

// MaxRamMb returns the maximum RAM limit (max of tier and all active addons)
func (t *TierWithAddons) MaxRamMb() int64 {
	max := t.Tier.MaxRamMb
	for _, addon := range t.Addons {
		if addon.MaxRamMb > max {
			max = addon.MaxRamMb
		}
	}
	return max
}

// DiskMb returns the disk limit from the tier (addons don't modify this)
func (t *TierWithAddons) DiskMb() int64 {
	return t.Tier.DiskMb
}

// MaxLengthHours returns the max length hours from the tier (addons don't modify this)
func (t *TierWithAddons) MaxLengthHours() int64 {
	return t.Tier.MaxLengthHours
}
