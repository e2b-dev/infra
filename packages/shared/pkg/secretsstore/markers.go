package secretsstore

import "strings"

const (
	// MarkerPrefix opens a customer-secret marker inside a transform header
	// value. The runtime substitutes the named secret's current value at
	// sandbox egress.
	MarkerPrefix = "${e2b.secrets."

	// MaxMarkerNames bounds the unique canonical names one domain's transform
	// set may reference. It is the resolver contract's per-call name limit,
	// enforced at sandbox-create admission and defended again at resolution.
	MaxMarkerNames = 32
)

// AppendMarkerNames scans one header value and appends the canonical names of
// its well-formed markers to names, deduplicating through seen. A marker that
// is unterminated or does not canonicalize is skipped: it is left to fail its
// own header at substitution, exactly as the runtime treats it.
func AppendMarkerNames(names []string, seen map[string]struct{}, value string) []string {
	remaining := value
	for {
		_, after, found := strings.Cut(remaining, MarkerPrefix)
		if !found {
			return names
		}

		marker, rest, closed := strings.Cut(after, "}")
		if !closed {
			return names
		}
		remaining = rest

		name, err := NormalizeName(marker)
		if err != nil {
			continue
		}

		if _, duplicate := seen[name]; duplicate {
			continue
		}

		seen[name] = struct{}{}
		names = append(names, name)
	}
}
