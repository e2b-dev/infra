package secretsstore

import (
	"errors"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

const maxNameBytes = 128

var ErrInvalidName = errors.New("secret name is invalid")

// NormalizeName returns the canonical spelling of a customer secret name.
// The fixed error never includes the rejected input because names are
// confidential selectors.
func NormalizeName(input string) (string, error) {
	name := strings.TrimSpace(input)
	if len(name) == 0 || len(name) > maxNameBytes {
		return "", ErrInvalidName
	}

	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return "", ErrInvalidName
		}
	}

	name = strings.ToLower(name)
	if strings.HasPrefix(name, id.SecretIDPrefix) {
		return "", ErrInvalidName
	}

	return name, nil
}
