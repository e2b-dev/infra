package api_templates

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
)

// One base image per supported distro family. Provisioning installs the family's
// own package names under set -e, links its own init binary and regenerates its
// own CA bundle, so a build that reaches ready with the start and ready commands
// executed proves the whole profile resolves and that envd boots under it.
func TestTemplateBuildDistroFamilies(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		templateName string
		fromImage    string
	}{
		{
			name:         "Debian family",
			templateName: "test-distro-ubuntu",
			fromImage:    "ubuntu:26.04",
		},
		{
			name:         "RPM family",
			templateName: "test-distro-fedora",
			fromImage:    "fedora:44",
		},
		{
			name:         "Arch",
			templateName: "test-distro-arch",
			fromImage:    "archlinux:base",
		},
		{
			name:         "Alpine on OpenRC",
			templateName: "test-distro-alpine",
			fromImage:    "alpine:3.24",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logMessages []string
			logHandler := func(alias string, entry api.BuildLogEntry) {
				logMessages = append(logMessages, entry.Message)
				defaultBuildLogHandler(t)(alias, entry)
			}

			buildConfig := api.TemplateBuildStartV2{
				Force:     new(ForceBaseBuild),
				FromImage: new(tc.fromImage),
				Steps:     new([]api.TemplateStep{}),
				StartCmd:  new("echo 'Sandbox started'"),
				ReadyCmd:  new("sleep 1"),
			}

			outcome := runTemplateBuild(t, tc.templateName, buildConfig, logHandler)
			require.True(t, outcome.ready, "Build failed: %s", outcome.reason)

			for _, expectedLog := range []string{"[start] [stdout]: Sandbox started", "Template is ready"} {
				assert.True(t, slices.ContainsFunc(logMessages, func(msg string) bool {
					return strings.Contains(msg, expectedLog)
				}), "Expected log message not found: %s", expectedLog)
			}
		})
	}
}

// An image from a distro E2B doesn't provision must be rejected while
// provisioning, and the reason must reach the customer instead of a bare exit
// status. Oracle Linux declares ID_LIKE=fedora, so this also covers the
// rejection guard running before the ID_LIKE fallback.
func TestTemplateBuildUnsupportedDistro(t *testing.T) {
	t.Parallel()

	buildConfig := api.TemplateBuildStartV2{
		Force:     new(ForceBaseBuild),
		FromImage: new("oraclelinux:9"),
		Steps:     new([]api.TemplateStep{}),
	}

	outcome := runTemplateBuild(t, "test-distro-unsupported", buildConfig, defaultBuildLogHandler(t))
	require.False(t, outcome.ready, "Build of an unsupported distro must fail")
	assert.Contains(t, outcome.reason, "ID='ol' is not supported")
	assert.Contains(t, outcome.reason, "Sandboxes boot E2B's kernel")
}
