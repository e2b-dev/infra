package cmdutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
)

type TemplateInfo struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Aliases    []string `json:"aliases"`
	Names      []string `json:"names"`
}

// ResolveTemplateID fetches the build ID for a template by ID, alias, or name.
// Requires E2B_API_KEY; respects E2B_DOMAIN for non-production environments.
func ResolveTemplateID(input string) (string, error) {
	apiKey := os.Getenv("E2B_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("E2B_API_KEY environment variable required for -template flag")
	}

	apiURL := "https://api.e2b.dev/templates"
	if domain := os.Getenv("E2B_DOMAIN"); domain != "" {
		apiURL = fmt.Sprintf("https://api.%s/templates", domain)
	}

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch templates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var templates []TemplateInfo
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	var match *TemplateInfo
	var availableAliases []string

	for i := range templates {
		t := &templates[i]
		availableAliases = append(availableAliases, t.Aliases...)

		if t.TemplateID == input {
			match = t

			break
		}

		if slices.Contains(t.Aliases, input) {
			match = t

			break
		}

		if slices.Contains(t.Names, input) {
			match = t

			break
		}
	}

	if match == nil {
		return "", fmt.Errorf("template %q not found. Available aliases: %s", input, strings.Join(availableAliases, ", "))
	}

	if match.BuildID == "" || match.BuildID == NilUUID {
		return "", fmt.Errorf("template %q has no successful build", input)
	}

	return match.BuildID, nil
}
