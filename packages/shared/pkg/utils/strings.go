package utils

import "strings"

const ellipses = "..."

// FirstNonEmpty returns the first value that is non-empty after trimming
// surrounding whitespace, or "" if every value is empty.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= len(ellipses) {
		return string(runes[:maxLen])
	}

	return string(runes[:maxLen-len(ellipses)]) + ellipses
}
