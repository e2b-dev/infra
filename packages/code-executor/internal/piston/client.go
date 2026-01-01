package piston

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Runtime represents a runtime available in Piston
type Runtime struct {
	Language string   `json:"language"`
	Version  string   `json:"version"`
	Aliases  []string `json:"aliases"`
}

// RuntimesResponse represents the response from /api/v2/runtimes
type RuntimesResponse map[string][]Runtime

// Client is a client for Piston API
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
	
	// Cache for runtimes
	runtimesCache     RuntimesResponse
	runtimesCacheOnce sync.Once
	runtimesCacheMu   sync.RWMutex
}

// NewClient creates a new Piston client
func NewClient(baseURL string, logger *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// ExecuteRequest represents a request to execute code
type ExecuteRequest struct {
	Language string `json:"language"`
	Version  string `json:"version"`
	Files    []File `json:"files"`
	Stdin    string `json:"stdin,omitempty"`
	Timeout  int    `json:"timeout,omitempty"` // timeout in seconds
}

// File represents a file to execute
type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ExecuteResponse represents the response from Piston
type ExecuteResponse struct {
	Language string `json:"language"`
	Version  string `json:"version"`
	Run      Run    `json:"run"`
	Compile Compile `json:"compile,omitempty"`
}

// Run contains execution results
type Run struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Code     int    `json:"code"`
	Signal   string `json:"signal,omitempty"`
	Output   string `json:"output"`
}

// Compile contains compilation results
type Compile struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	Output string `json:"output"`
}

// GetRuntimes fetches available runtimes from Piston API
func (c *Client) GetRuntimes(ctx context.Context) (RuntimesResponse, error) {
	// Try to use cache first
	c.runtimesCacheMu.RLock()
	if c.runtimesCache != nil && len(c.runtimesCache) > 0 {
		cache := c.runtimesCache
		c.runtimesCacheMu.RUnlock()
		return cache, nil
	}
	c.runtimesCacheMu.RUnlock()

	// Fetch from API
	httpReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v2/runtimes", c.baseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("piston API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Piston API returns an array of Runtime objects, not a map
	var runtimesArray []Runtime
	if err := json.Unmarshal(body, &runtimesArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert array to map grouped by language
	runtimes := make(RuntimesResponse)
	for _, rt := range runtimesArray {
		runtimes[rt.Language] = append(runtimes[rt.Language], rt)
	}

	// Update cache
	c.runtimesCacheMu.Lock()
	c.runtimesCache = runtimes
	c.runtimesCacheMu.Unlock()

	return runtimes, nil
}

// Execute executes code using Piston API
func (c *Client) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	// Map language names to Piston API language names
	language, version, err := c.mapLanguageToPiston(ctx, req.Language, req.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to map language: %w", err)
	}
	
	// Update language and version
	req.Language = language
	req.Version = version

	// Default timeout if not specified
	if req.Timeout == 0 {
		req.Timeout = 10
	}

	// Create request body
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/v2/execute", c.baseURL), bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("piston API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var executeResp ExecuteResponse
	if err := json.Unmarshal(body, &executeResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &executeResp, nil
}

// mapLanguageToPiston maps user-friendly language names to Piston API language names and versions
// Returns (pistonLanguage, version, error)
func (c *Client) mapLanguageToPiston(ctx context.Context, language, requestedVersion string) (string, string, error) {
	// Get available runtimes
	runtimes, err := c.GetRuntimes(ctx)
	if err != nil {
		c.logger.Warn("Failed to fetch runtimes, using fallback", zap.Error(err))
		// Fallback to hardcoded mapping if API is unavailable
		return c.mapLanguageToPistonFallback(language), c.getDefaultVersionFallback(language, requestedVersion), nil
	}

	// Normalize language name (lowercase)
	languageLower := language

	// Try to find exact match first
	if versions, ok := runtimes[languageLower]; ok && len(versions) > 0 {
		version := requestedVersion
		if version == "" {
			// Use the first (usually latest) version
			version = versions[0].Version
		} else {
			// Check if requested version exists
			found := false
			for _, v := range versions {
				if v.Version == version {
					found = true
					break
				}
			}
			if !found {
				// Use the first available version if requested not found
				version = versions[0].Version
			}
		}
		return languageLower, version, nil
	}

	// Try to find by alias
	for lang, versions := range runtimes {
		for _, v := range versions {
			for _, alias := range v.Aliases {
				if alias == languageLower {
					version := requestedVersion
					if version == "" {
						version = v.Version
					}
					return lang, version, nil
				}
			}
		}
	}

	// Fallback to common mappings
	mappedLang := c.mapLanguageToPistonFallback(language)
	if versions, ok := runtimes[mappedLang]; ok && len(versions) > 0 {
		version := requestedVersion
		if version == "" {
			version = versions[0].Version
		}
		return mappedLang, version, nil
	}

	// If still not found, return as-is and let Piston handle it
	version := requestedVersion
	if version == "" {
		version = "*"
	}
	return languageLower, version, nil
}

// mapLanguageToPistonFallback provides fallback mapping when API is unavailable
func (c *Client) mapLanguageToPistonFallback(language string) string {
	languageMap := map[string]string{
		"javascript": "node",
		"cpp":        "gcc",
		"c":          "gcc",
	}
	
	if mapped, ok := languageMap[language]; ok {
		return mapped
	}
	
	return language
}

// getDefaultVersionFallback provides fallback version when API is unavailable
func (c *Client) getDefaultVersionFallback(language, requestedVersion string) string {
	if requestedVersion != "" {
		return requestedVersion
	}
	
	// Default to "*" to let Piston choose
	return "*"
}

