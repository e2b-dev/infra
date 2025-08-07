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
	t.Parallel()

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
				FromImage: utils.ToPtr("ubuntu:22.04"),
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
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, tc.buildConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildENV(t *testing.T) {
	t.Parallel()

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
				FromImage: utils.ToPtr("ubuntu:22.04"),
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
				FromImage: utils.ToPtr("ubuntu:22.04"),
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
				FromImage: utils.ToPtr("ubuntu:22.04"),
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
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, tc.buildConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildWORKDIR(t *testing.T) {
	t.Parallel()

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
				FromImage: utils.ToPtr("ubuntu:22.04"),
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
				FromImage: utils.ToPtr("ubuntu:22.04"),
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
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, tc.buildConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildCache(t *testing.T) {
	t.Parallel()

	alias := "test-ubuntu-cache"
	template := api.TemplateBuildStartV2{
		FromImage: utils.ToPtr("ubuntu:22.04"),
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

func TestTemplateBuildFromTemplate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		baseTemplate    string
		baseConfig      api.TemplateBuildStartV2
		derivedTemplate string
		derivedConfig   api.TemplateBuildStartV2
	}{
		{
			name:         "Basic fromTemplate functionality",
			baseTemplate: "test-ubuntu-base-template",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"BASE_VAR", "base_value"}),
					},
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/app"}),
					},
				}),
			},
			derivedTemplate: "test-ubuntu-derived-template",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-template"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"DERIVED_VAR", "derived_value"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{": \"${BASE_VAR:?BASE_VAR not inherited}\"; [[ \"$BASE_VAR\" == \"base_value\" ]] || exit 1; [[ \"$(pwd)\" == \"/app\" ]] || exit 2; [[ \"$DERIVED_VAR\" == \"derived_value\" ]] || exit 3; echo 'Inheritance verification passed'"}),
					},
				}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// First build the base template
			assert.True(t, buildTemplate(t, tc.baseTemplate, tc.baseConfig, defaultBuildLogHandler(t)))

			// Then build the derived template from the base template
			assert.True(t, buildTemplate(t, tc.derivedTemplate, tc.derivedConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildFromTemplateCommandOverride(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		baseTemplate    string
		baseConfig      api.TemplateBuildStartV2
		derivedTemplate string
		derivedConfig   api.TemplateBuildStartV2
	}{
		{
			name:         "Start command override in derived template",
			baseTemplate: "test-ubuntu-base-override-start",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"BASE_VAR", "base_value"}),
					},
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/app/base"}),
					},
				}),
				// Base start command - fails if override_check.txt exists (meaning it's running in derived context)
				StartCmd: utils.ToPtr("[[ ! -f /override_check.txt ]] || exit 97; echo 'base_command_executed'"),
				ReadyCmd: utils.ToPtr("sleep 5"),
			},
			derivedTemplate: "test-ubuntu-derived-override-start",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-override-start"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"DERIVED_VAR", "derived_value"}),
					},
					{
						Type: "WORKDIR",
						Args: utils.ToPtr([]string{"/app/derived"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"echo 'override_expected' > /override_check.txt"}),
					},
				}),
				// Override the base start command - simple success proves override worked
				StartCmd: utils.ToPtr("exit 0"),
				ReadyCmd: utils.ToPtr("sleep 5"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// First build the base template
			assert.True(t, buildTemplate(t, tc.baseTemplate, tc.baseConfig, defaultBuildLogHandler(t)))

			// Then build the derived template from the base template
			assert.True(t, buildTemplate(t, tc.derivedTemplate, tc.derivedConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildFromTemplateInheritance(t *testing.T) {
	t.Parallel()

	baseTemplate := "test-ubuntu-inheritance-base"
	derivedTemplate := "test-ubuntu-inheritance-derived"

	// Base template with ENV and WORKDIR settings
	baseConfig := api.TemplateBuildStartV2{
		Force:     utils.ToPtr(ForceBaseBuild),
		FromImage: utils.ToPtr("ubuntu:22.04"),
		Steps: utils.ToPtr([]api.TemplateStep{
			{
				Type:  "ENV",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"BASE_ENV", "inherited_value"}),
			},
			{
				Type:  "ENV",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"OVERRIDE_VAR", "base_value"}),
			},
			{
				Type:  "WORKDIR",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"/base/workdir"}),
			},
		}),
	}

	// Derived template that tests inheritance and override
	derivedConfig := api.TemplateBuildStartV2{
		Force:        utils.ToPtr(true),
		FromTemplate: utils.ToPtr(baseTemplate),
		Steps: utils.ToPtr([]api.TemplateStep{
			{
				Type: "ENV",
				Args: utils.ToPtr([]string{"OVERRIDE_VAR", "derived_value"}),
			},
			{
				Type: "RUN",
				Args: utils.ToPtr([]string{
					// Test ENV inheritance
					": \"${BASE_ENV:?BASE_ENV is not set or empty}\"; " +
						"[[ \"$BASE_ENV\" == \"inherited_value\" ]] || exit 1; " +
						// Test ENV override
						"[[ \"$OVERRIDE_VAR\" == \"derived_value\" ]] || exit 2; " +
						// Test WORKDIR inheritance
						"[[ \"$(pwd)\" == \"/base/workdir\" ]] || exit 3; " +
						"echo 'All inheritance tests passed'",
				}),
			},
		}),
	}

	// First build the base template
	assert.True(t, buildTemplate(t, baseTemplate, baseConfig, defaultBuildLogHandler(t)))

	// Then build the derived template from the base template
	assert.True(t, buildTemplate(t, derivedTemplate, derivedConfig, defaultBuildLogHandler(t)))
}

func TestTemplateBuildFromTemplateStartCommand(t *testing.T) {
	t.Parallel()

	baseTemplate := "test-ubuntu-start-base"
	derivedTemplate := "test-ubuntu-start-derived"

	// Base template with ENV and WORKDIR for start command inheritance
	baseConfig := api.TemplateBuildStartV2{
		Force:     utils.ToPtr(ForceBaseBuild),
		FromImage: utils.ToPtr("ubuntu:22.04"),
		Steps: utils.ToPtr([]api.TemplateStep{
			{
				Type:  "ENV",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"START_ENV", "start_value"}),
			},
			{
				Type:  "WORKDIR",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"/start/workdir"}),
			},
		}),
	}

	// Derived template with start command that tests ENV and WORKDIR inheritance
	derivedConfig := api.TemplateBuildStartV2{
		Force:        utils.ToPtr(true),
		FromTemplate: utils.ToPtr(baseTemplate),
		StartCmd: utils.ToPtr(
			// Test ENV inheritance in start command
			": \"${START_ENV:?START_ENV is not set or empty}\"; " +
				"[[ \"$START_ENV\" == \"start_value\" ]] || exit 1; " +
				// Test WORKDIR inheritance in start command
				"[[ \"$(pwd)\" == \"/start/workdir\" ]] || exit 2; " +
				"echo 'Start command inheritance tests passed'",
		),
		ReadyCmd: utils.ToPtr("sleep 5"),
	}

	// First build the base template
	assert.True(t, buildTemplate(t, baseTemplate, baseConfig, defaultBuildLogHandler(t)))

	// Then build the derived template from the base template
	assert.True(t, buildTemplate(t, derivedTemplate, derivedConfig, defaultBuildLogHandler(t)))
}

func TestTemplateBuildFromTemplateBaseCommandsInheritance(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		baseTemplate    string
		baseConfig      api.TemplateBuildStartV2
		derivedTemplate string
		derivedConfig   api.TemplateBuildStartV2
	}{
		{
			name:         "Start command inherited from base template uses original base context",
			baseTemplate: "test-ubuntu-base-with-start",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"BASE_VAR", "base_value"}),
					},
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/app/base"}),
					},
				}),
				// Start command runs with base template context (not derived modifications)
				StartCmd: utils.ToPtr(": \"${BASE_VAR:?BASE_VAR not set}\"; [[ \"$BASE_VAR\" == \"base_value\" ]] || exit 1; [[ \"$(pwd)\" == \"/app/base\" ]] || exit 2; echo \"Base start command runs with original base context\""),
				ReadyCmd: utils.ToPtr("sleep 5"),
			},
			derivedTemplate: "test-ubuntu-derived-with-inheritance",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-with-start"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "WORKDIR",
						Args: utils.ToPtr([]string{"/app/derived"}), // Override base workdir
					},
				}),
				// No StartCmd/ReadyCmd - inherit from base, runs with original base context
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// First build the base template
			assert.True(t, buildTemplate(t, tc.baseTemplate, tc.baseConfig, defaultBuildLogHandler(t)))

			// Then build the derived template from the base template
			assert.True(t, buildTemplate(t, tc.derivedTemplate, tc.derivedConfig, defaultBuildLogHandler(t)))
		})
	}
}

func TestTemplateBuildFromTemplateLayered(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                 string
		baseTemplate         string
		baseConfig           api.TemplateBuildStartV2
		intermediateTemplate string
		intermediateConfig   api.TemplateBuildStartV2
		finalTemplate        string
		finalConfig          api.TemplateBuildStartV2
	}{
		{
			name:         "Three-level template inheritance with ENV accumulation",
			baseTemplate: "test-ubuntu-layered-base",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"LEVEL", "base"}),
					},
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"BASE_VAR", "base_value"}),
					},
				}),
			},
			intermediateTemplate: "test-ubuntu-layered-intermediate",
			intermediateConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-layered-base"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"LEVEL", "intermediate"}),
					},
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"INTERMEDIATE_VAR", "intermediate_value"}),
					},
				}),
			},
			finalTemplate: "test-ubuntu-layered-final",
			finalConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-layered-intermediate"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"LEVEL", "final"}),
					},
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"FINAL_VAR", "final_value"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{
							"[[ \"$LEVEL\" == \"final\" ]] || exit 1; " +
								"[[ \"$BASE_VAR\" == \"base_value\" ]] || exit 2; " +
								"[[ \"$INTERMEDIATE_VAR\" == \"intermediate_value\" ]] || exit 3; " +
								"[[ \"$FINAL_VAR\" == \"final_value\" ]] || exit 4",
						}),
					},
				}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Build base template
			assert.True(t, buildTemplate(t, tc.baseTemplate, tc.baseConfig, defaultBuildLogHandler(t)))

			// Build intermediate template from base
			assert.True(t, buildTemplate(t, tc.intermediateTemplate, tc.intermediateConfig, defaultBuildLogHandler(t)))

			// Build final template from intermediate
			assert.True(t, buildTemplate(t, tc.finalTemplate, tc.finalConfig, defaultBuildLogHandler(t)))
		})
	}
}
