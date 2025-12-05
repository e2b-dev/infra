package id

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dchest/uniuri"
)

var caseInsensitiveAlphabet = []byte("abcdefghijklmnopqrstuvwxyz1234567890")

const DefaultTag = "latest"

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

// ParseTemplateIDOrAliasWithTag parses a template ID or alias with an optional tag in the format "templateID:tag"
// Returns the template ID/alias and the tag (or nil if no tag was specified)
func ParseTemplateIDOrAliasWithTag(input string) (templateIDOrAlias string, tag *string, err error) {
	input = strings.TrimSpace(input)

	// Split by colon to separate template ID and tag
	parts := strings.SplitN(input, ":", 2)

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

	return templateIDOrAlias, tag, nil
}
