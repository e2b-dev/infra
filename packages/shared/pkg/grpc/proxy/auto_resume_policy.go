package proxygrpc

import "strings"

// AutoResumePolicyFromString normalizes the metadata value to an enum.
func AutoResumePolicyFromString(value string) AutoResumePolicy {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "any":
		return AutoResumePolicy_AUTO_RESUME_POLICY_ANY
	case "null", "":
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	default:
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	}
}

// AutoResumePolicyToString returns the canonical string value for persistence.
func AutoResumePolicyToString(policy AutoResumePolicy) string {
	switch policy {
	case AutoResumePolicy_AUTO_RESUME_POLICY_ANY:
		return "any"
	case AutoResumePolicy_AUTO_RESUME_POLICY_NULL:
		return "null"
	default:
		return "null"
	}
}

// NormalizeAutoResumePolicy folds unspecified policies into null.
func NormalizeAutoResumePolicy(policy AutoResumePolicy) AutoResumePolicy {
	switch policy {
	case AutoResumePolicy_AUTO_RESUME_POLICY_ANY,
		AutoResumePolicy_AUTO_RESUME_POLICY_NULL:
		return policy
	default:
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	}
}
