package utils

import (
	"fmt"
	"os"
	"strings"
)

// archAliases normalizes common architecture names to Go convention.
var archAliases = map[string]string{
	"amd64":   "amd64",
	"x86_64":  "amd64",
	"arm64":   "arm64",
	"aarch64": "arm64",
}

const defaultArch = "amd64"

// TargetArch returns the target architecture for binary paths and OCI platform.
// If TARGET_ARCH is set, it is normalized to Go convention ("amd64" or "arm64");
// otherwise defaults to "amd64" for backwards compatibility with existing deployments.
func TargetArch() string {
	if arch := os.Getenv("TARGET_ARCH"); arch != "" {
		if normalized, ok := archAliases[arch]; ok {
			return normalized
		}

		fmt.Fprintf(os.Stderr, "WARNING: unrecognized TARGET_ARCH=%q, falling back to %s\n", arch, defaultArch)

		return defaultArch
	}

	return defaultArch
}

// RequiredEnv returns the value of the environment variable for key if it is set, non-empty and not only whitespace.
// It panics otherwise.
//
// Pass the envUsageMsg to describe what the environment variable is used for. This will be used in the error message.
func RequiredEnv(key string, envUsageMsg string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		panic(fmt.Sprintf("Required environment variable \"%s\" (%s) is not set. Please set it to a non-empty value.", key, envUsageMsg))
	}

	if value == "" {
		panic(fmt.Sprintf("Required environment variable \"%s\" (%s) is set but it is empty. Please set it to a non-empty value.", key, envUsageMsg))
	}

	if strings.TrimSpace(value) == "" {
		panic(fmt.Sprintf("Required environment variable \"%s\" (%s) is set but it contains only whitespace. Please set it to a non-empty value.", key, envUsageMsg))
	}

	return value
}

// OptionalEnv returns the value of the environment variable for key if it is set, non-empty and not only whitespace.
//
// Pass the envUsageMsg to describe what the environment variable is used for.
// This will be used in the message that is printed if the environment variable is not returned.
func OptionalEnv(key string, envUsageMsg string) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok {
		fmt.Fprintf(os.Stderr, "Optional environment variable \"%s\" (%s) is not set.\n", key, envUsageMsg)

		return "", false
	}

	if strings.TrimSpace(value) == "" {
		fmt.Fprintf(os.Stderr, "Optional environment variable \"%s\" (%s) is set but it contains only whitespace.\n", key, envUsageMsg)

		return "", false
	}

	return value, true
}
