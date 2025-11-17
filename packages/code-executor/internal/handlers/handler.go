package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/code-executor/internal/piston"
	"github.com/e2b-dev/infra/packages/code-executor/internal/worker"
)

// Handler handles HTTP requests
type Handler struct {
	pistonClient *piston.Client
	workerPool   *worker.Pool
	logger       *zap.Logger
}

// NewHandler creates a new handler
func NewHandler(pistonClient *piston.Client, workerPool *worker.Pool, logger *zap.Logger) *Handler {
	return &Handler{
		pistonClient: pistonClient,
		workerPool:   workerPool,
		logger:       logger,
	}
}

// ExecuteRequest represents the request body for /execute endpoint
type ExecuteRequest struct {
	Lang    string `json:"lang" binding:"required"`
	Code    string `json:"code" binding:"required"`
	Timeout int    `json:"timeout"` // timeout in seconds
}

// ExecuteResponse represents the response for /execute endpoint
type ExecuteResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// Execute handles POST /execute
func (h *Handler) Execute(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default timeout
	if req.Timeout == 0 {
		req.Timeout = 10
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(req.Timeout+1)*time.Second)
	defer cancel()

	// Execute code using worker pool
	result := h.workerPool.Execute(ctx, func(ctx context.Context) (interface{}, error) {
		return h.executeCode(ctx, req.Lang, req.Code, "", req.Timeout)
	})

	if result.Error != nil {
		h.logger.Error("Failed to execute code", zap.Error(result.Error))
		c.JSON(http.StatusInternalServerError, ExecuteResponse{
			Stdout: "",
			Stderr: result.Error.Error(),
		})
		return
	}

	execResult := result.Data.(*ExecuteResponse)
	c.JSON(http.StatusOK, execResult)
}

// Test represents a single test case
type Test struct {
	ID    int    `json:"id" binding:"required"`
	Input string `json:"input" binding:"required"`
}

// TestsRequest represents the request body for /tests endpoint
type TestsRequest struct {
	Lang    string `json:"lang" binding:"required"`
	Code    string `json:"code" binding:"required"`
	Timeout int    `json:"timeout"` // timeout in seconds
	Tests   []Test `json:"tests" binding:"required"`
}

// TestResponse represents the response for a single test
type TestResponse struct {
	ID    int    `json:"id"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// Tests handles POST /tests
func (h *Handler) Tests(c *gin.Context) {
	var req TestsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default timeout
	if req.Timeout == 0 {
		req.Timeout = 10
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(req.Timeout+1)*time.Second)
	defer cancel()

	// Execute all tests in parallel using worker pool
	results := make([]TestResponse, len(req.Tests))
	var wg sync.WaitGroup
	wg.Add(len(req.Tests))

	// Submit all tasks to worker pool
	for i, test := range req.Tests {
		i, test := i, test // capture loop variables
		h.workerPool.ExecuteAsync(ctx, func(ctx context.Context) (interface{}, error) {
			return h.executeCode(ctx, req.Lang, req.Code, test.Input, req.Timeout)
		}, func(result worker.Result) {
			defer wg.Done()
			if result.Error != nil {
				results[i] = TestResponse{
					ID:    test.ID,
					Stdout: "",
					Stderr: result.Error.Error(),
				}
			} else {
				execResult := result.Data.(*ExecuteResponse)
				results[i] = TestResponse{
					ID:    test.ID,
					Stdout: execResult.Stdout,
					Stderr: execResult.Stderr,
				}
			}
		})
	}

	// Wait for all tasks to complete
	wg.Wait()

	c.JSON(http.StatusOK, results)
}

// executeCode executes code using Piston client
func (h *Handler) executeCode(ctx context.Context, lang, code, stdin string, timeout int) (*ExecuteResponse, error) {
	// Determine file name based on language
	fileName := getFileName(lang)

	req := piston.ExecuteRequest{
		Language: lang,
		Files: []piston.File{
			{
				Name:    fileName,
				Content: code,
			},
		},
		Stdin:   stdin,
		Timeout: timeout * 1000, // Convert to milliseconds for Piston
	}

	resp, err := h.pistonClient.Execute(ctx, req)
	if err != nil {
		// Check if it's a timeout error
		if ctx.Err() == context.DeadlineExceeded {
			return &ExecuteResponse{
				Stdout: "",
				Stderr: "Execution timeout exceeded",
			}, nil
		}
		return nil, err
	}

	// Check if execution timed out
	if resp.Run.Code != 0 && resp.Run.Signal == "SIGKILL" {
		return &ExecuteResponse{
			Stdout: resp.Run.Stdout,
			Stderr: "Execution timeout exceeded",
		}, nil
	}

	return &ExecuteResponse{
		Stdout: resp.Run.Stdout,
		Stderr: resp.Run.Stderr,
	}, nil
}

// getFileName returns the appropriate file name for a language
func getFileName(lang string) string {
	fileNames := map[string]string{
		"python":     "main.py",
		"javascript": "main.js",
		"typescript": "main.ts",
		"java":       "Main.java",
		"cpp":        "main.cpp",
		"c":          "main.c",
		"go":         "main.go",
		"rust":       "main.rs",
		"ruby":       "main.rb",
		"php":        "main.php",
	}

	if fileName, ok := fileNames[lang]; ok {
		return fileName
	}

	return "main"
}

