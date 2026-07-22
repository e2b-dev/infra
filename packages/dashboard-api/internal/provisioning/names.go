package provisioning

import (
	"strings"
	"unicode"
)

func defaultTeamNameFromOIDCUserName(name *string) string {
	if name == nil || strings.TrimSpace(*name) == "" {
		return "Personal Project"
	}

	return capitalizeFirstLetter(firstWord(*name)) + "'s Project"
}

func firstWord(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

func capitalizeFirstLetter(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return ""
	}

	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}
