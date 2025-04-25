package utils

func SubstringMax(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
