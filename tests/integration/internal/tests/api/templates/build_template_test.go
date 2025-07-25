package api_templates

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

const (
	EnableDebugLogs = false

	ForceBaseBuild = false
	BuildTimeout   = 5 * time.Minute
)

type BuildLogHandler func(alias string, entry api.BuildLogEntry)

func buildTemplate(
	tb testing.TB,
	templateAlias string,
	data api.TemplateBuildStartV2,
	logHandler BuildLogHandler,
) bool {
	tb.Helper()

	ctx, cancel := context.WithTimeout(tb.Context(), BuildTimeout)
	defer cancel()

	c := setup.GetAPIClient()

	// Request build
	resp, err := c.PostV2TemplatesWithResponse(ctx, api.TemplateBuildRequestV2{
		Alias:    templateAlias,
		CpuCount: utils.ToPtr[int32](2),
		MemoryMB: utils.ToPtr[int32](1024),
	}, setup.WithAPIKey())
	require.NoError(tb, err)
	require.Equal(tb, http.StatusAccepted, resp.StatusCode())
	require.NotNil(tb, resp.JSON202)

	// Start build
	startResp, err := c.PostV2TemplatesTemplateIDBuildsBuildIDWithResponse(
		ctx,
		resp.JSON202.TemplateID,
		resp.JSON202.BuildID,
		data,
		setup.WithAPIKey(),
	)
	require.NoError(tb, err)
	assert.Equal(tb, http.StatusAccepted, startResp.StatusCode())

	logLevel := api.LogLevelInfo
	if EnableDebugLogs {
		logLevel = api.LogLevelDebug
	}

	// Check build status
	offset := 0
	for {
		statusResp, err := c.GetTemplatesTemplateIDBuildsBuildIDStatusWithResponse(
			ctx,
			resp.JSON202.TemplateID,
			resp.JSON202.BuildID,
			&api.GetTemplatesTemplateIDBuildsBuildIDStatusParams{
				LogsOffset: utils.ToPtr(int32(offset)),
				Level:      &logLevel,
			},
			setup.WithAPIKey(),
		)
		require.NoError(tb, err)
		assert.Equal(tb, http.StatusOK, statusResp.StatusCode())
		require.NotNil(tb, statusResp.JSON200)

		offset += len(statusResp.JSON200.LogEntries)
		for _, entry := range statusResp.JSON200.LogEntries {
			logHandler(templateAlias, entry)
		}

		switch statusResp.JSON200.Status {
		case api.TemplateBuildStatusReady:
			tb.Log("Build completed successfully")
			return true
		case api.TemplateBuildStatusError:
			tb.Fatalf("Build failed: %v", statusResp.JSON200.Reason)
			return false
		}

		time.Sleep(time.Second)
	}
}

func defaultBuildLogHandler(tb testing.TB) BuildLogHandler {
	tb.Helper()

	return func(alias string, entry api.BuildLogEntry) {
		tb.Logf("%s: [%s] %s", alias, entry.Level, entry.Message)
	}
}

func TestTemplateBuildRUN(t *testing.T) {
	testCases := []struct {
		name         string
		templateName string
		buildConfig  api.TemplateBuildStartV2
	}{
		{
			name:         "Single RUN command",
			templateName: "test-ubuntu-run",
			buildConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: "ubuntu:22.04",
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "RUN",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"echo 'Hello, World!'"}),
					},
				}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, buildTemplate(t, tc.templateName, tc.buildConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildENV(t *testing.T) {
	testCases := []struct {
		name         string
		templateName string
		buildConfig  api.TemplateBuildStartV2
	}{
		{
			name:         "ENV variable persistence",
			templateName: "test-ubuntu-env",
			buildConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: "ubuntu:22.04",
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"ENV_VAR", "Hello, World!"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{": \"${ENV_VAR:?ENV_VAR is not set or empty}\"; echo \"$ENV_VAR\""}),
					},
				}),
			},
		},
		{
			name:         "ENV variable persistence for start command",
			templateName: "test-ubuntu-env-start",
			buildConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: "ubuntu:22.04",
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"ENV_VAR", "Hello, World!"}),
					},
				}),
				StartCmd: utils.ToPtr(": \"${ENV_VAR:?ENV_VAR is not set or empty}\"; echo \"$ENV_VAR\""),
				ReadyCmd: utils.ToPtr("sleep 5"),
			},
		},
		{
			name:         "ENV variable recursive",
			templateName: "test-ubuntu-env-recursive",
			buildConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: "ubuntu:22.04",
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"PATH", "${PATH}:/my/path"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"[[ \"$PATH\" == *:/my/path ]] || exit 1"}),
					},
				}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, buildTemplate(t, tc.templateName, tc.buildConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildWORKDIR(t *testing.T) {
	testCases := []struct {
		name         string
		templateName string
		buildConfig  api.TemplateBuildStartV2
	}{
		{
			name:         "WORKDIR persistence",
			templateName: "test-ubuntu-workdir-persistence",
			buildConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: "ubuntu:22.04",
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/app"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"[[ \"$(pwd)\" == \"/app\" ]] || exit 1"}),
					},
				}),
			},
		},
		{
			name:         "WORKDIR persistence in start command",
			templateName: "test-ubuntu-workdir-start",
			buildConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: "ubuntu:22.04",
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/app"}),
					},
				}),
				StartCmd: utils.ToPtr("[[ \"$(pwd)\" == \"/app\" ]] || exit 1"),
				ReadyCmd: utils.ToPtr("sleep 5"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, buildTemplate(t, tc.templateName, tc.buildConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildCache(t *testing.T) {
	alias := "test-ubuntu-cache"
	template := api.TemplateBuildStartV2{
		FromImage: "ubuntu:22.04",
		Steps: utils.ToPtr([]api.TemplateStep{
			{
				Type: "ENV",
				Args: utils.ToPtr([]string{"ENV_VAR", "Hello, World!"}),
			},
			{
				Type: "RUN",
				Args: utils.ToPtr([]string{": \"${ENV_VAR:?ENV_VAR is not set or empty}\"; echo \"$ENV_VAR\""}),
			},
		}),
	}

	assert.True(t, buildTemplate(t, alias, template, defaultBuildLogHandler(t)))

	messages := make([]string, 0)
	assert.True(t, buildTemplate(t, alias, template, func(alias string, entry api.BuildLogEntry) {
		messages = append(messages, entry.Message)
		defaultBuildLogHandler(t)(alias, entry)
	}))
	assert.Condition(t, func() bool {
		for _, msg := range messages {
			if strings.Contains(msg, "CACHED [builder 1/2] ENV ENV_VAR Hello, World!") {
				return true
			}
		}
		return false
	}, "Expected to contain cached ENV layer")
}
