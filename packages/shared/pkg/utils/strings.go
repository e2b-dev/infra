package utils

const ellipses = "..."

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
