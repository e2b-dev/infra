package piston

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Client is a client for Piston API
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
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

// Execute executes code using Piston API
func (c *Client) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	// Default version for common languages
	if req.Version == "" {
		req.Version = c.getDefaultVersion(req.Language)
	}

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

// getDefaultVersion returns default version for a language
func (c *Client) getDefaultVersion(language string) string {
	versions := map[string]string{
		"python": "3.10.0",
		"javascript": "18.15.0",
		"typescript": "5.0.3",
		"java": "15.0.2",
		"cpp": "10.2.0",
		"c": "10.2.0",
		"go": "1.21.0",
		"rust": "1.70.0",
		"ruby": "3.2.0",
		"php": "8.2.0",
	}

	if version, ok := versions[language]; ok {
		return version
	}

	// Default to latest if not found
	return "*"
}

