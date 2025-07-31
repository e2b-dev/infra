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
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"echo 'Base template setup'"}),
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

	testCases := []struct {
		name            string
		baseTemplate    string
		baseConfig      api.TemplateBuildStartV2
		derivedTemplate string
		derivedConfig   api.TemplateBuildStartV2
	}{
		{
			name:         "ENV variable inheritance from base template",
			baseTemplate: "test-ubuntu-base-env-inheritance",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"BASE_ENV", "inherited_value"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"echo 'Base ENV set'"}),
					},
				}),
			},
			derivedTemplate: "test-ubuntu-derived-env-inheritance",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-env-inheritance"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{": \"${BASE_ENV:?BASE_ENV is not set or empty}\"; echo \"Inherited: $BASE_ENV\""}),
					},
				}),
			},
		},
		{
			name:         "WORKDIR inheritance from base template",
			baseTemplate: "test-ubuntu-base-workdir-inheritance",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/base/workdir"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"echo 'Base workdir set'"}),
					},
				}),
			},
			derivedTemplate: "test-ubuntu-derived-workdir-inheritance",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-workdir-inheritance"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"[[ \"$(pwd)\" == \"/base/workdir\" ]] || exit 1"}),
					},
				}),
			},
		},
		{
			name:         "ENV variable override in derived template",
			baseTemplate: "test-ubuntu-base-env-override",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"OVERRIDE_VAR", "base_value"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"echo 'Base value set'"}),
					},
				}),
			},
			derivedTemplate: "test-ubuntu-derived-env-override",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-env-override"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"OVERRIDE_VAR", "derived_value"}),
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"[[ \"$OVERRIDE_VAR\" == \"derived_value\" ]] || exit 1"}),
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

func TestTemplateBuildFromTemplateStartCommand(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		baseTemplate    string
		baseConfig      api.TemplateBuildStartV2
		derivedTemplate string
		derivedConfig   api.TemplateBuildStartV2
	}{
		{
			name:         "Start command with ENV inheritance from base template",
			baseTemplate: "test-ubuntu-base-start-env",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"START_ENV", "start_value"}),
					},
				}),
			},
			derivedTemplate: "test-ubuntu-derived-start-env",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-start-env"),
				StartCmd:     utils.ToPtr(": \"${START_ENV:?START_ENV is not set or empty}\"; echo \"Start command with: $START_ENV\""),
				ReadyCmd:     utils.ToPtr("sleep 5"),
			},
		},
		{
			name:         "Start command with WORKDIR inheritance from base template",
			baseTemplate: "test-ubuntu-base-start-workdir",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/start/workdir"}),
					},
				}),
			},
			derivedTemplate: "test-ubuntu-derived-start-workdir",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-start-workdir"),
				StartCmd:     utils.ToPtr("[[ \"$(pwd)\" == \"/start/workdir\" ]] || exit 1"),
				ReadyCmd:     utils.ToPtr("sleep 5"),
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
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"echo 'Derived template setup complete'"}),
					},
				}),
				// No StartCmd/ReadyCmd - inherit from base, runs with original base context
			},
		},
		{
			name:         "Base template commands with variable override and complex context",
			baseTemplate: "test-ubuntu-base-override-context",
			baseConfig: api.TemplateBuildStartV2{
				Force:     utils.ToPtr(ForceBaseBuild),
				FromImage: utils.ToPtr("ubuntu:22.04"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"APP_NAME", "myapp"}),
					},
					{
						Type:  "ENV",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"VERSION", "1.0.0"}),
					},
					{
						Type:  "WORKDIR",
						Force: utils.ToPtr(true),
						Args:  utils.ToPtr([]string{"/opt/app"}),
					},
				}),
				// Base start command uses only base template context (original context where it was defined)
				StartCmd: utils.ToPtr(": \"${APP_NAME:?APP_NAME not set}\"; [[ \"$APP_NAME\" == \"myapp\" ]] || exit 1; [[ \"$VERSION\" == \"1.0.0\" ]] || exit 2; [[ \"$(pwd)\" == \"/opt/app\" ]] || exit 3; echo \"Base template context verification passed\""),
				ReadyCmd: utils.ToPtr("sleep 5"),
			},
			derivedTemplate: "test-ubuntu-derived-override-context",
			derivedConfig: api.TemplateBuildStartV2{
				Force:        utils.ToPtr(true),
				FromTemplate: utils.ToPtr("test-ubuntu-base-override-context"),
				Steps: utils.ToPtr([]api.TemplateStep{
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"VERSION", "2.0.0"}), // Override base version
					},
					{
						Type: "ENV",
						Args: utils.ToPtr([]string{"CONFIG_FILE", "/etc/myapp.conf"}), // New variable
					},
					{
						Type: "WORKDIR",
						Args: utils.ToPtr([]string{"/opt/app/production"}), // Change workdir
					},
					{
						Type: "RUN",
						Args: utils.ToPtr([]string{"mkdir -p /etc && echo 'config=production' > /etc/myapp.conf"}),
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
