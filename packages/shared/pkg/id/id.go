package id

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dchest/uniuri"
	"github.com/google/uuid"
)

var (
	caseInsensitiveAlphabet = []byte("abcdefghijklmnopqrstuvwxyz1234567890")
	identifierRegex         = regexp.MustCompile(`^[a-z0-9-_]+$`)
	tagRegex                = regexp.MustCompile(`^[a-z0-9-_.]+$`)
)

const (
	DefaultTag         = "default"
	TagSeparator       = ":"
	NamespaceSeparator = "/"
)

func Generate() string {
	return uniuri.NewLenChars(uniuri.UUIDLen, caseInsensitiveAlphabet)
}

func cleanAndValidate(value, name string, re *regexp.Regexp) (string, error) {
	cleaned := strings.ToLower(strings.TrimSpace(value))
	if !re.MatchString(cleaned) {
		return "", fmt.Errorf("invalid %s: %s", name, value)
	}

	return cleaned, nil
}

func validateTag(tag string) (string, error) {
	cleanedTag, err := cleanAndValidate(tag, "tag", tagRegex)
	if err != nil {
		return "", err
	}

	// Prevent tags from being a UUID
	_, err = uuid.Parse(cleanedTag)
	if err == nil {
		return "", errors.New("tag cannot be a UUID")
	}

	return cleanedTag, nil
}

func ValidateAndDeduplicateTags(tags []string) ([]string, error) {
	seen := make(map[string]struct{})

	for _, tag := range tags {
		cleanedTag, err := validateTag(tag)
		if err != nil {
			return nil, fmt.Errorf("invalid tag '%s': %w", tag, err)
		}

		seen[cleanedTag] = struct{}{}
	}

	return slices.Collect(maps.Keys(seen)), nil
}

// SplitIdentifier splits "namespace/alias" into its parts.
// Returns nil namespace for bare aliases, pointer for explicit namespace.
func SplitIdentifier(identifier string) (namespace *string, alias string) {
	before, after, found := strings.Cut(identifier, NamespaceSeparator)
	if !found {
		return nil, before
	}

	return &before, after
}

// ParseName parses and validates "namespace/alias:tag" or "alias:tag".
// Returns the cleaned identifier (namespace/alias or alias) and optional tag.
// All components are validated and normalized (lowercase, trimmed).
func ParseName(input string) (identifier string, tag *string, err error) {
	input = strings.TrimSpace(input)

	// Extract raw parts
	identifierPart, tagPart, hasTag := strings.Cut(input, TagSeparator)
	namespacePart, aliasPart := SplitIdentifier(identifierPart)

	// Validate tag
	if hasTag {
		validated, err := cleanAndValidate(tagPart, "tag", tagRegex)
		if err != nil {
			return "", nil, err
		}
		if !strings.EqualFold(validated, DefaultTag) {
			tag = &validated
		}
	}

	// Validate namespace
	if namespacePart != nil {
		validated, err := cleanAndValidate(*namespacePart, "namespace", identifierRegex)
		if err != nil {
			return "", nil, err
		}
		namespacePart = &validated
	}

	// Validate alias
	aliasPart, err = cleanAndValidate(aliasPart, "template ID", identifierRegex)
	if err != nil {
		return "", nil, err
	}

	// Build identifier
	if namespacePart != nil {
		identifier = WithNamespace(*namespacePart, aliasPart)
	} else {
		identifier = aliasPart
	}

	return identifier, tag, nil
}

// WithNamespace returns identifier with the given namespace prefix.
func WithNamespace(namespace, alias string) string {
	return namespace + NamespaceSeparator + alias
}

// ExtractAlias returns just the alias portion from an identifier (namespace/alias or alias).
func ExtractAlias(identifier string) string {
	_, alias := SplitIdentifier(identifier)

	return alias
}

// ValidateNamespaceMatchesTeam checks if an explicit namespace in the identifier matches the team's slug.
// Returns an error if the namespace doesn't match.
// If the identifier has no explicit namespace, returns nil (valid).
func ValidateNamespaceMatchesTeam(identifier, teamSlug string) error {
	namespace, _ := SplitIdentifier(identifier)
	if namespace != nil && *namespace != teamSlug {
		return fmt.Errorf("namespace '%s' must match your team '%s'", *namespace, teamSlug)
	}

	return nil
}
