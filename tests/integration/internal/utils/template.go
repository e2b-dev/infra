package utils

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

const (
	DefaultBuildTimeout = 5 * time.Minute
	DefaultCPUCount     = int32(2)
	DefaultMemoryMB     = int32(1024)
)

// BuildLogHandler is a function that handles build log entries
type BuildLogHandler func(alias string, entry api.BuildLogEntry)

// TemplateBuildOptions contains options for building a template
type TemplateBuildOptions struct {
	Name        string
	Tags        *[]string
	CPUCount    *int32
	MemoryMB    *int32
	BuildData   api.TemplateBuildStartV2
	Timeout     time.Duration
	LogHandler  BuildLogHandler
	EnableDebug bool
	ReqEditors  []api.RequestEditorFn
}

// DefaultBuildLogHandler returns a default log handler that logs to the test logger
func DefaultBuildLogHandler(tb testing.TB) BuildLogHandler {
	tb.Helper()

	return func(alias string, entry api.BuildLogEntry) {
		tb.Logf("%s: [%s] %s", alias, entry.Level, entry.Message)
	}
}

// NoOpBuildLogHandler returns a log handler that does nothing
func NoOpBuildLogHandler() BuildLogHandler {
	return func(_ string, _ api.BuildLogEntry) {
		// No-op
	}
}

// BuildTemplate builds a template with the given options and waits for it to complete
func BuildTemplate(tb testing.TB, opts TemplateBuildOptions) *api.TemplateRequestResponseV3 {
	tb.Helper()

	// Set defaults
	if opts.CPUCount == nil {
		opts.CPUCount = utils.ToPtr(DefaultCPUCount)
	}
	if opts.MemoryMB == nil {
		opts.MemoryMB = utils.ToPtr(DefaultMemoryMB)
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultBuildTimeout
	}
	if opts.LogHandler == nil {
		opts.LogHandler = NoOpBuildLogHandler()
	}

	ctx, cancel := context.WithTimeout(tb.Context(), opts.Timeout)
	defer cancel()

	reqEditors := append(opts.ReqEditors, setup.WithTestsUserAgent())

	// Request build
	template := requestTemplateBuild(tb, opts.Name, opts.CPUCount, opts.MemoryMB, opts.Tags, reqEditors...)

	// Start build
	startTemplateBuild(tb, template.TemplateID, template.BuildID, opts.BuildData, reqEditors...)

	// Wait for build to complete
	WaitForBuildCompletion(tb, ctx, template.TemplateID, template.BuildID, opts.Name, opts.LogHandler, opts.EnableDebug, reqEditors...)

	return template
}

// WaitForBuildCompletion waits for a template build to complete
func WaitForBuildCompletion(
	tb testing.TB,
	ctx context.Context,
	templateID string,
	buildID string,
	name string,
	logHandler BuildLogHandler,
	enableDebug bool,
	reqEditors ...api.RequestEditorFn,
) {
	tb.Helper()

	c := setup.GetAPIClient()

	logLevel := api.LogLevelInfo
	if enableDebug {
		logLevel = api.LogLevelDebug
	}

	// Check build status
	offset := 0
	for {
		select {
		case <-ctx.Done():
			tb.Fatal("Build timeout exceeded")

			return
		default:
		}

		statusResp, err := c.GetTemplatesTemplateIDBuildsBuildIDStatusWithResponse(
			ctx,
			templateID,
			buildID,
			&api.GetTemplatesTemplateIDBuildsBuildIDStatusParams{
				LogsOffset: utils.ToPtr(int32(offset)),
				Level:      &logLevel,
			},
			reqEditors...,
		)
		require.NoError(tb, err)
		assert.Equal(tb, http.StatusOK, statusResp.StatusCode(), string(statusResp.Body))
		require.NotNil(tb, statusResp.JSON200, string(statusResp.Body))

		offset += len(statusResp.JSON200.LogEntries)
		for _, entry := range statusResp.JSON200.LogEntries {
			logHandler(name, entry)
		}

		switch statusResp.JSON200.Status {
		case api.TemplateBuildStatusReady:
			tb.Log("Build completed successfully")

			return
		case api.TemplateBuildStatusError:
			tb.Fatalf("Build failed: %v", safeValue(statusResp.JSON200.Reason))

			return
		}

		time.Sleep(time.Second)
	}
}

// requestTemplateBuild requests a template build without waiting for completion
func requestTemplateBuild(
	tb testing.TB,
	name string,
	cpuCount, memoryMB *int32,
	tags *[]string,
	reqEditors ...api.RequestEditorFn,
) *api.TemplateRequestResponseV3 {
	tb.Helper()

	ctx := tb.Context()
	c := setup.GetAPIClient()

	if cpuCount == nil {
		cpuCount = utils.ToPtr(DefaultCPUCount)
	}
	if memoryMB == nil {
		memoryMB = utils.ToPtr(DefaultMemoryMB)
	}

	req := api.TemplateBuildRequestV3{
		Name:     utils.ToPtr(name),
		Tags:     tags,
		CpuCount: cpuCount,
		MemoryMB: memoryMB,
	}

	resp, err := c.PostV3TemplatesWithResponse(ctx, req, reqEditors...)
	require.NoError(tb, err)
	require.Equal(tb, http.StatusAccepted, resp.StatusCode())
	require.NotNil(tb, resp.JSON202)

	return resp.JSON202
}

// startTemplateBuild starts a template build with the given data
func startTemplateBuild(
	tb testing.TB,
	templateID string,
	buildID string,
	buildData api.TemplateBuildStartV2,
	reqEditors ...api.RequestEditorFn,
) {
	tb.Helper()

	ctx := tb.Context()
	c := setup.GetAPIClient()

	startResp, err := c.PostV2TemplatesTemplateIDBuildsBuildIDWithResponse(
		ctx,
		templateID,
		buildID,
		buildData,
		reqEditors...,
	)
	require.NoError(tb, err)
	require.Equal(tb, http.StatusAccepted, startResp.StatusCode())
}

// safeValue safely dereferences a pointer, returning zero value if nil
func safeValue[T any](item *T) T {
	if item != nil {
		return *item
	}
	var t T

	return t
}

// BuildSimpleTemplate builds a simple template with Ubuntu 22.04 base image
func BuildSimpleTemplate(tb testing.TB, name string, reqEditors ...api.RequestEditorFn) *api.TemplateRequestResponseV3 {
	tb.Helper()

	opts := TemplateBuildOptions{
		Name: name,
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		ReqEditors: reqEditors,
	}

	return BuildTemplate(tb, opts)
}
