package proxygrpc

import (
	context "context"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"go.uber.org/zap"
)

// AutoResumePolicyFromString normalizes the metadata value to an enum.
func AutoResumePolicyFromString(value string) AutoResumePolicy {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "any":
		return AutoResumePolicy_AUTO_RESUME_POLICY_ANY
	case "null", "", "off":
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	default:
		logger.L().Warn(context.Background(), "Received unrecognized auto-resume policy value, defaulting to null", zap.String("value", value))
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
		logger.L().Warn(context.Background(), "Received unrecognized auto-resume policy enum value, defaulting to null", zap.Int32("value", int32(policy)))
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
		logger.L().Warn(context.Background(), "Received unrecognized auto-resume policy enum value, defaulting to null", zap.Int32("value", int32(policy)))
		return AutoResumePolicy_AUTO_RESUME_POLICY_NULL
	}
}
