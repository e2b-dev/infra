package proxygrpc

import "strings"

// AutoResumePolicyFromString normalizes the metadata value to an enum.
func AutoResumePolicyFromString(value string) AutoResumePolicy {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "any":
		return AutoResumePolicy_AUTO_RESUME_POLICY_ANY
	case "authed":
		return AutoResumePolicy_AUTO_RESUME_POLICY_AUTHED
	case "null", "":
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	default:
		return AutoResumePolicy_AUTO_RESUME_POLICY_UNSPECIFIED
	}
}

// AutoResumePolicyToString returns the canonical string value for persistence.
func AutoResumePolicyToString(policy AutoResumePolicy) string {
	switch policy {
	case AutoResumePolicy_AUTO_RESUME_POLICY_ANY:
		return "any"
	case AutoResumePolicy_AUTO_RESUME_POLICY_AUTHED:
		return "authed"
	case AutoResumePolicy_AUTO_RESUME_POLICY_NULL:
		return "null"
	default:
		return "null"
	}
}

// AutoResumePolicyFromMetadata extracts and normalizes the auto_resume value.
func AutoResumePolicyFromMetadata(metadata map[string]string) AutoResumePolicy {
	if metadata == nil {
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	}

	return AutoResumePolicyFromString(metadata["auto_resume"])
}
