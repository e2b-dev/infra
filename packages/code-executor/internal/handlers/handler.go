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

	// Validate timeout
	if req.Timeout < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "timeout must be positive"})
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

	// Validate timeout
	if req.Timeout < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "timeout must be positive"})
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
	fileName := h.getFileName(ctx, lang)

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
// Tries to determine extension dynamically, falls back to common mappings
func (h *Handler) getFileName(ctx context.Context, lang string) string {
	// Try to get runtimes to determine file extension
	runtimes, err := h.pistonClient.GetRuntimes(ctx)
	if err == nil {
		// Try to find language in runtimes
		langLower := lang
		if versions, ok := runtimes[langLower]; ok && len(versions) > 0 {
			// Use extension mapping based on language name
			if ext := getExtensionForLanguage(langLower); ext != "" {
				return "main" + ext
			}
		}
	}

	// Fallback to static mapping
	return getFileNameFallback(lang)
}

// getExtensionForLanguage returns file extension for a language
func getExtensionForLanguage(lang string) string {
	// Common file extensions mapping
	extensions := map[string]string{
		"python":     ".py",
		"node":       ".js",
		"javascript": ".js",
		"typescript": ".ts",
		"java":       ".java",
		"gcc":        ".c", // Default for gcc, but could be .cpp
		"cpp":        ".cpp",
		"c":          ".c",
		"go":         ".go",
		"rust":       ".rs",
		"ruby":       ".rb",
		"php":        ".php",
		"perl":       ".pl",
		"lua":        ".lua",
		"r":          ".r",
		"swift":      ".swift",
		"kotlin":     ".kt",
		"scala":      ".scala",
		"clojure":    ".clj",
		"haskell":    ".hs",
		"erlang":     ".erl",
		"elixir":     ".ex",
		"crystal":    ".cr",
		"nim":        ".nim",
		"dart":       ".dart",
		"zig":        ".zig",
		"v":          ".v",
		"ocaml":      ".ml",
		"fsharp":     ".fsx",
		"csharp":     ".cs",
		"vbnet":      ".vb",
		"bash":       ".sh",
		"powershell": ".ps1",
		"sql":        ".sql",
		"julia":      ".jl",
		"octave":     ".m",
		"matlab":     ".m",
		"fortran":    ".f90",
		"cobol":      ".cob",
		"pascal":     ".pas",
		"prolog":     ".pl",
		"lisp":       ".lisp",
		"scheme":     ".scm",
		"racket":     ".rkt",
		"factor":     ".factor",
		"forth":      ".fs",
		"tcl":        ".tcl",
		"awk":        ".awk",
		"sed":        ".sed",
		"groovy":     ".groovy",
		"ceylon":     ".ceylon",
		"coffeescript": ".coffee",
		"livescript":   ".ls",
		"reason":       ".re",
		"elm":          ".elm",
		"purescript":  ".purs",
		"idris":        ".idr",
		"agda":         ".agda",
		"lean":         ".lean",
		"coq":          ".v",
		"isabelle":     ".thy",
		"alloy":        ".als",
		"z3":           ".smt2",
		"cvc4":         ".smt2",
		"yices":        ".smt2",
		"boo":          ".boo",
		"io":           ".io",
		"ioke":         ".ik",
		"nu":           ".nu",
		"ooc":          ".ooc",
		"parrot":       ".pir",
		"perl6":        ".p6",
		"raku":         ".raku",
		"red":          ".red",
		"rexx":         ".rexx",
		"ring":         ".ring",
		"smalltalk":    ".st",
		"unlambda":     ".unl",
		"vala":         ".vala",
		"verilog":      ".v",
		"vhdl":         ".vhd",
		"wren":         ".wren",
		"x10":          ".x10",
		"xeora":        ".xeora",
		"yorick":       ".yor",
		"zsh":          ".zsh",
	}

	if ext, ok := extensions[lang]; ok {
		return ext
	}

	return ""
}

// getFileNameFallback returns file name using static mapping
func getFileNameFallback(lang string) string {
	fileNames := map[string]string{
		"python":     "main.py",
		"javascript": "main.js",
		"node":       "main.js",
		"typescript": "main.ts",
		"java":       "Main.java",
		"cpp":        "main.cpp",
		"c":          "main.c",
		"gcc":        "main.c",
		"go":         "main.go",
		"rust":       "main.rs",
		"ruby":       "main.rb",
		"php":        "main.php",
	}

	if fileName, ok := fileNames[lang]; ok {
		return fileName
	}

	// Try to get extension dynamically
	if ext := getExtensionForLanguage(lang); ext != "" {
		return "main" + ext
	}

	// Ultimate fallback
	return "main"
}

