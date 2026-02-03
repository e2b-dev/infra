package handlers

import (
	"fmt"
	"strings"
)

func validateAutoResumeMetadata(metadata map[string]string) error {
	if metadata == nil {
		return nil
	}

	value, ok := metadata["auto_resume"]
	if !ok {
		return nil
	}

	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "any", "authed", "null":
		metadata["auto_resume"] = normalized
		return nil
	case "":
		return fmt.Errorf("auto_resume must be one of any, authed, null")
	default:
		return fmt.Errorf("auto_resume must be one of any, authed, null")
	}
}
