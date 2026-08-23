package networktransform

import (
	"errors"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/secretsstore"
)

const (
	identityTokenPrefix  = "${e2b.identity.tokens."
	customerSecretPrefix = "${e2b.secrets."

	// MaxMarkerNames bounds the unique canonical customer-secret names one
	// domain's transform set may reference.
	MaxMarkerNames = 32
)

// The parser error never includes the header value or placeholder name.
var errMalformedPlaceholder = errors.New("network transform placeholder is malformed")

// PlaceholderKind identifies the value source for a parsed placeholder.
type PlaceholderKind uint8

const (
	// PlaceholderIdentityToken selects a persisted named identity token.
	PlaceholderIdentityToken PlaceholderKind = iota + 1
	// PlaceholderCustomerSecret selects a customer secret.
	PlaceholderCustomerSecret
)

// Placeholder is a typed, half-open byte span in the original header value.
type Placeholder struct {
	Kind  PlaceholderKind
	Name  string
	Start int
	End   int
}

// ParsePlaceholders parses known placeholders in value from left to right.
// Text outside the two known prefixes is opaque.
func ParsePlaceholders(value string) ([]Placeholder, error) {
	var placeholders []Placeholder

	for cursor := 0; cursor < len(value); {
		relativeStart, kind, prefix := nextPlaceholder(value[cursor:])
		if relativeStart < 0 {
			return placeholders, nil
		}

		start := cursor + relativeStart
		nameStart := start + len(prefix)
		relativeEnd := strings.IndexByte(value[nameStart:], '}')
		if relativeEnd < 0 {
			return nil, errMalformedPlaceholder
		}

		nameEnd := nameStart + relativeEnd
		if nestedStart, _, _ := nextPlaceholder(value[nameStart:nameEnd]); nestedStart >= 0 {
			return nil, errMalformedPlaceholder
		}

		name := value[nameStart:nameEnd]
		if name == "" {
			return nil, errMalformedPlaceholder
		}
		if kind == PlaceholderCustomerSecret {
			canonical, err := secretsstore.NormalizeName(name)
			if err != nil {
				return nil, errMalformedPlaceholder
			}
			name = canonical
		}

		end := nameEnd + 1
		placeholders = append(placeholders, Placeholder{
			Kind:  kind,
			Name:  name,
			Start: start,
			End:   end,
		})
		cursor = end
	}

	return placeholders, nil
}

func nextPlaceholder(value string) (int, PlaceholderKind, string) {
	identityStart := strings.Index(value, identityTokenPrefix)
	secretStart := strings.Index(value, customerSecretPrefix)

	switch {
	case identityStart < 0 && secretStart < 0:
		return -1, 0, ""
	case secretStart < 0 || (identityStart >= 0 && identityStart < secretStart):
		return identityStart, PlaceholderIdentityToken, identityTokenPrefix
	default:
		return secretStart, PlaceholderCustomerSecret, customerSecretPrefix
	}
}
