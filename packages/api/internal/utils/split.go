package utils

import (
	"fmt"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

func ShortID(compositeID string) (string, error) {
	parts := strings.Split(compositeID, "-")
	if len(parts) > 2 {
		return "", fmt.Errorf("invalid sandbox ID: %q", compositeID)
	}

	sandboxID := compositeID
	if len(parts) == 2 {
		sandboxID = parts[0]
	}

	if err := id.ValidateSandboxID(sandboxID); err != nil {
		return "", err
	}

	return sandboxID, nil
}
