package artifacts_registry

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// AWS ECR tag constraints
	MaxTagLength = 128
	// AWS ECR allows alphanumeric characters, hyphens, underscores, periods, and forward slashes
	// We'll be more restrictive to ensure safety across different systems
	TagSeparator = "-"
)

var (
	// AWS ECR tag validation regex - allows alphanumeric, hyphens, underscores, periods
	// We exclude forward slashes for safety in composite tags
	validTagCharRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	
	// Template ID validation - more restrictive to prevent injection
	validTemplateIdRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	
	// Build ID validation - similar to template ID
	validBuildIdRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// Tag validation errors
var (
	ErrInvalidTemplateId = errors.New("invalid template id format")
	ErrInvalidBuildId    = errors.New("invalid build id format")
	ErrTagTooLong        = errors.New("composite tag exceeds AWS ECR limit")
	ErrEmptyTemplateId   = errors.New("template id cannot be empty")
	ErrEmptyBuildId      = errors.New("build id cannot be empty")
)

// CompositeTagOptions holds configuration for composite tag generation
type CompositeTagOptions struct {
	// MaxLength sets the maximum allowed tag length (default: MaxTagLength)
	MaxLength int
	// TruncateStrategy defines how to handle tags that are too long
	TruncateStrategy TruncateStrategy
}

type TruncateStrategy int

const (
	// TruncateNone returns an error if tag is too long
	TruncateNone TruncateStrategy = iota
	// TruncateEnd truncates from the end
	TruncateEnd
	// TruncateMiddle truncates from the middle, preserving start and end
	TruncateMiddle
)

// DefaultCompositeTagOptions returns default options for composite tag generation
func DefaultCompositeTagOptions() CompositeTagOptions {
	return CompositeTagOptions{
		MaxLength:        MaxTagLength,
		TruncateStrategy: TruncateNone,
	}
}

// ValidateTemplateId validates that a template ID is safe for use in tags
func ValidateTemplateId(templateId string) error {
	if templateId == "" {
		return ErrEmptyTemplateId
	}
	
	if !validTemplateIdRegex.MatchString(templateId) {
		return fmt.Errorf("%w: contains invalid characters (only alphanumeric, hyphens, and underscores allowed)", ErrInvalidTemplateId)
	}
	
	return nil
}

// ValidateBuildId validates that a build ID is safe for use in tags
func ValidateBuildId(buildId string) error {
	if buildId == "" {
		return ErrEmptyBuildId
	}
	
	if !validBuildIdRegex.MatchString(buildId) {
		return fmt.Errorf("%w: contains invalid characters (only alphanumeric, hyphens, and underscores allowed)", ErrInvalidBuildId)
	}
	
	return nil
}

// GenerateCompositeTag creates a composite tag from templateId and buildId
func GenerateCompositeTag(templateId, buildId string) (string, error) {
	return GenerateCompositeTagWithOptions(templateId, buildId, DefaultCompositeTagOptions())
}

// GenerateCompositeTagWithOptions creates a composite tag with custom options
func GenerateCompositeTagWithOptions(templateId, buildId string, options CompositeTagOptions) (string, error) {
	// Validate inputs
	if err := ValidateTemplateId(templateId); err != nil {
		return "", err
	}
	
	if err := ValidateBuildId(buildId); err != nil {
		return "", err
	}
	
	// Create composite tag
	compositeTag := fmt.Sprintf("%s%s%s", templateId, TagSeparator, buildId)
	
	// Check length and apply truncation strategy if needed
	if len(compositeTag) > options.MaxLength {
		switch options.TruncateStrategy {
		case TruncateNone:
			return "", fmt.Errorf("%w: tag length %d exceeds maximum %d", ErrTagTooLong, len(compositeTag), options.MaxLength)
		case TruncateEnd:
			compositeTag = truncateEnd(compositeTag, options.MaxLength)
		case TruncateMiddle:
			compositeTag = truncateMiddle(templateId, buildId, options.MaxLength)
		}
	}
	
	// Final validation of the generated tag
	if !validTagCharRegex.MatchString(compositeTag) {
		return "", fmt.Errorf("generated composite tag contains invalid characters: %s", compositeTag)
	}
	
	return compositeTag, nil
}

// ParseCompositeTag extracts templateId and buildId from a composite tag
func ParseCompositeTag(compositeTag string) (templateId, buildId string, err error) {
	if compositeTag == "" {
		return "", "", errors.New("composite tag cannot be empty")
	}
	
	// Find the last occurrence of the separator to handle cases where
	// templateId or buildId might contain the separator character
	lastSepIndex := strings.LastIndex(compositeTag, TagSeparator)
	if lastSepIndex == -1 {
		return "", "", errors.New("invalid composite tag format: separator not found")
	}
	
	templateId = compositeTag[:lastSepIndex]
	buildId = compositeTag[lastSepIndex+1:]
	
	if templateId == "" {
		return "", "", errors.New("template id cannot be empty in composite tag")
	}
	
	if buildId == "" {
		return "", "", errors.New("build id cannot be empty in composite tag")
	}
	
	return templateId, buildId, nil
}

// IsCompositeTag checks if a tag appears to be in composite format
func IsCompositeTag(tag string) bool {
	_, _, err := ParseCompositeTag(tag)
	return err == nil
}

// truncateEnd truncates the tag from the end to fit within maxLength
func truncateEnd(tag string, maxLength int) string {
	if len(tag) <= maxLength {
		return tag
	}
	return tag[:maxLength]
}

// truncateMiddle truncates from the middle while preserving template and build identifiers
func truncateMiddle(templateId, buildId string, maxLength int) string {
	separatorLen := len(TagSeparator)
	
	// If even the separator doesn't fit, return truncated version
	if maxLength <= separatorLen {
		return TagSeparator[:maxLength]
	}
	
	availableLen := maxLength - separatorLen
	
	// Try to preserve at least some characters from both parts
	minPartLen := 3
	
	if len(templateId) + len(buildId) <= availableLen {
		// Should not happen, but handle gracefully
		return fmt.Sprintf("%s%s%s", templateId, TagSeparator, buildId)
	}
	
	// If one part is very short, preserve it entirely
	if len(templateId) <= minPartLen {
		buildIdLen := availableLen - len(templateId)
		if buildIdLen > len(buildId) {
			buildIdLen = len(buildId)
		}
		return fmt.Sprintf("%s%s%s", templateId, TagSeparator, buildId[:buildIdLen])
	}
	
	if len(buildId) <= minPartLen {
		templateIdLen := availableLen - len(buildId)
		if templateIdLen > len(templateId) {
			templateIdLen = len(templateId)
		}
		return fmt.Sprintf("%s%s%s", templateId[:templateIdLen], TagSeparator, buildId)
	}
	
	// Both parts are long, split available space roughly equally
	halfLen := availableLen / 2
	templateIdLen := halfLen
	buildIdLen := availableLen - halfLen
	
	// Adjust if one part is shorter than its allocation
	if len(templateId) < templateIdLen {
		templateIdLen = len(templateId)
		buildIdLen = availableLen - templateIdLen
	}
	
	if len(buildId) < buildIdLen {
		buildIdLen = len(buildId)
		templateIdLen = availableLen - buildIdLen
	}
	
	return fmt.Sprintf("%s%s%s", 
		templateId[:templateIdLen], 
		TagSeparator, 
		buildId[:buildIdLen])
}