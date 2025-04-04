package utils

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

const maxSandboxID = "zzzzzzzzzzzzzzzzzzzz"

// extend the api.ListedSandbox with a timestamp to use for pagination
type PaginatedSandbox struct {
	api.ListedSandbox
	PaginationTimestamp time.Time `json:"-"`
}

func (p *PaginatedSandbox) GenerateCursor() string {
	cursor := fmt.Sprintf("%s__%s", p.PaginationTimestamp.Format(time.RFC3339Nano), p.SandboxID)
	return base64.URLEncoding.EncodeToString([]byte(cursor))
}

func ParseNextToken(token *string) (time.Time, string, error) {
	if token != nil && *token != "" {
		cursorTime, cursorID, err := ParseCursor(*token)
		if err != nil {
			return time.Time{}, "", err
		}

		return cursorTime, cursorID, nil
	}

	// default to all sandboxes (older than now) and always lexically after any sandbox ID (the sort is descending)
	return time.Now(), maxSandboxID, nil
}

func ParseMetadata(metadata *string) (*map[string]string, error) {
	// Parse metadata filter (query) if provided
	var metadataFilter *map[string]string
	if metadata != nil {
		parsedMetadataFilter, err := parseFilters(*metadata)
		if err != nil {
			zap.L().Error("Error parsing metadata", zap.Error(err))

			return nil, fmt.Errorf("error parsing metadata: %w", err)
		}

		metadataFilter = &parsedMetadataFilter
	}

	return metadataFilter, nil
}

func ParseCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("error decoding cursor: %w", err)
	}

	parts := strings.Split(string(decoded), "__")
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}

	cursorTime, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid timestamp format in cursor: %w", err)
	}

	return cursorTime, parts[1], nil
}

func FilterBasedOnCursor(sandboxes []PaginatedSandbox, cursorTime time.Time, cursorID string, limit int32) []PaginatedSandbox {
	// Apply cursor-based filtering if cursor is provided
	var filteredSandboxes []PaginatedSandbox
	for _, sandbox := range sandboxes {
		// Take sandboxes with start time before cursor time OR
		// same start time but sandboxID greater than cursor ID (for stability)
		if sandbox.StartedAt.Before(cursorTime) ||
			(sandbox.StartedAt.Equal(cursorTime) && sandbox.SandboxID > cursorID) {
			filteredSandboxes = append(filteredSandboxes, sandbox)
		}
	}
	sandboxes = filteredSandboxes

	// Apply limit if provided (get limit + 1 for pagination if possible)
	if len(sandboxes) > int(limit) {
		sandboxes = sandboxes[:limit+1]
	}

	return sandboxes
}

func FilterSandboxesOnMetadata(sandboxes []PaginatedSandbox, metadata *map[string]string) []PaginatedSandbox {
	if metadata == nil {
		return sandboxes
	}

	// Filter instances to match all metadata
	n := 0
	for _, sbx := range sandboxes {
		if sbx.Metadata == nil {
			continue
		}

		matchesAll := true
		for key, value := range *metadata {
			if metadataValue, ok := (*sbx.Metadata)[key]; !ok || metadataValue != value {
				matchesAll = false
				break
			}
		}

		if matchesAll {
			sandboxes[n] = sbx
			n++
		}
	}

	// Trim slice
	sandboxes = sandboxes[:n]

	return sandboxes
}

func parseFilters(query string) (map[string]string, error) {
	query, err := url.QueryUnescape(query)
	if err != nil {
		return nil, fmt.Errorf("error when unescaping query: %w", err)
	}

	// Parse filters, both key and value are also unescaped
	filters := make(map[string]string)

	for _, filter := range strings.Split(query, "&") {
		parts := strings.Split(filter, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key value pair in query")
		}

		key, err := url.QueryUnescape(parts[0])
		if err != nil {
			return nil, fmt.Errorf("error when unescaping key: %w", err)
		}

		value, err := url.QueryUnescape(parts[1])
		if err != nil {
			return nil, fmt.Errorf("error when unescaping value: %w", err)
		}

		filters[key] = value
	}

	return filters, nil
}
