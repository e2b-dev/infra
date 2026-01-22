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

var caseInsensitiveAlphabet = []byte("abcdefghijklmnopqrstuvwxyz1234567890")

const (
	DefaultTag = "default"

	TagSeparator = ":"
)

func Generate() string {
	return uniuri.NewLenChars(uniuri.UUIDLen, caseInsensitiveAlphabet)
}

func cleanTemplateIDOrAlias(templateIDOrAlias string) (string, error) {
	cleanedTemplateIDOrAlias := strings.ToLower(strings.TrimSpace(templateIDOrAlias))
	ok, err := regexp.MatchString("^[a-z0-9-_]+$", cleanedTemplateIDOrAlias)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf("invalid template ID: %s", templateIDOrAlias)
	}

	return cleanedTemplateIDOrAlias, nil
}

func cleanTag(tag string) (string, error) {
	cleanedTag := strings.ToLower(strings.TrimSpace(tag))
	ok, err := regexp.MatchString("^[a-z0-9-_.]+$", cleanedTag)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf("invalid tag: %s", tag)
	}

	return cleanedTag, nil
}

// validateTag validates a single tag and returns the normalized (lowercased, trimmed) version.
func validateTag(tag string) (string, error) {
	cleanedTag, err := cleanTag(tag)
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

// ValidateAndDeduplicateTags validates each tag and returns a deduplicated slice of normalized (lowercased, trimmed) tags.
// Returns an error if any tag is invalid.
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

// ParseTemplateIDOrAliasWithTag parses a template ID or alias with an optional tag in the format "templateID:tag"
// Returns the template ID/alias and the tag (or nil if no tag was specified)
func ParseTemplateIDOrAliasWithTag(input string) (templateIDOrAlias string, tag *string, err error) {
	input = strings.TrimSpace(input)

	// Split by colon to separate template ID and tag
	parts := strings.SplitN(input, TagSeparator, 2)

	templateIDOrAlias = strings.ToLower(strings.TrimSpace(parts[0]))
	templateIDOrAlias, err = cleanTemplateIDOrAlias(templateIDOrAlias)
	if err != nil {
		return "", nil, err
	}

	// If there's a tag part, validate and return it
	if len(parts) == 2 {
		tagValue := strings.ToLower(strings.TrimSpace(parts[1]))
		tagValue, err = cleanTag(tagValue)
		if err != nil {
			return "", nil, err
		}

		tag = &tagValue
	}

	if tag != nil && strings.EqualFold(*tag, DefaultTag) {
		tag = nil
	}

	return templateIDOrAlias, tag, nil
}

func NameWithTag(name string, tag *string) string {
	if tag == nil {
		return name + TagSeparator + DefaultTag
	}

	return name + TagSeparator + *tag
}
