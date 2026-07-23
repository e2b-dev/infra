package api_templates

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
)

// TestTemplateBuildAlpine verifies template builds from Alpine base images.
func TestTemplateBuildAlpine(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		templateName string
		fromImage    string
	}{
		{"alpine:latest", "test-alpine-latest", "alpine:latest"},
		{"alpine:3.22", "test-alpine-3-22", "alpine:3.22"},
		{"alpine:3.21", "test-alpine-3-21", "alpine:3.21"},
		{"alpine:3.20", "test-alpine-3-20", "alpine:3.20"},
		{"alpine:3.19", "test-alpine-3-19", "alpine:3.19"},
		{"alpine:3.18", "test-alpine-3-18", "alpine:3.18"},
		{"alpine:3.17", "test-alpine-3-17", "alpine:3.17"},
		{"alpine:edge", "test-alpine-edge", "alpine:edge"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, api.TemplateBuildStartV2{
				Force:     new(ForceBaseBuild),
				FromImage: new(tc.fromImage),
				Steps: new([]api.TemplateStep{
					{
						Type:  "RUN",
						Force: new(true),
						Args:  new([]string{"echo 'Hello from Alpine'"}),
					},
				}),
			}, defaultBuildLogHandler(t)))
		})
	}
}

// TestTemplateBuildCentOS verifies template builds from CentOS base images.
func TestTemplateBuildCentOS(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		templateName string
		fromImage    string
	}{
		{"centos:7", "test-centos-7", "centos:7"},
		{"centos:8", "test-centos-8", "centos:8"},
		{"centos:stream8", "test-centos-stream8", "quay.io/centos/centos:stream8"},
		{"centos:stream9", "test-centos-stream9", "quay.io/centos/centos:stream9"},
		{"centos:stream10", "test-centos-stream10", "quay.io/centos/centos:stream10"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, api.TemplateBuildStartV2{
				Force:     new(ForceBaseBuild),
				FromImage: new(tc.fromImage),
				Steps: new([]api.TemplateStep{
					{
						Type:  "RUN",
						Force: new(true),
						Args:  new([]string{"echo 'Hello from CentOS'"}),
					},
				}),
			}, defaultBuildLogHandler(t)))
		})
	}
}

// TestTemplateBuildRHELCompat verifies template builds from RHEL-compatible distros.
func TestTemplateBuildRHELCompat(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		templateName string
		fromImage    string
	}{
		{"rockylinux:9", "test-rocky-9", "rockylinux/rockylinux:9"},
		{"rockylinux:8", "test-rocky-8", "rockylinux/rockylinux:8"},
		{"almalinux:9", "test-alma-9", "almalinux/almalinux:9"},
		{"almalinux:8", "test-alma-8", "almalinux/almalinux:8"},
		{"oraclelinux:9", "test-oracle-9", "oraclelinux:9"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, api.TemplateBuildStartV2{
				Force:     new(ForceBaseBuild),
				FromImage: new(tc.fromImage),
				Steps: new([]api.TemplateStep{
					{
						Type:  "RUN",
						Force: new(true),
						Args:  new([]string{"echo 'Hello from RHEL-compat'"}),
					},
				}),
			}, defaultBuildLogHandler(t)))
		})
	}
}

// TestTemplateBuildMultiDistroUSER verifies the USER step works across distro families.
func TestTemplateBuildMultiDistroUSER(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		templateName string
		fromImage    string
	}{
		{"debian-user", "test-debian-user-step", "ubuntu:22.04"},
		{"alpine-user", "test-alpine-user-step", "alpine:3.21"},
		{"rhel-user", "test-rhel-user-step", "rockylinux/rockylinux:9"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.True(t, buildTemplate(t, tc.templateName, api.TemplateBuildStartV2{
				Force:     new(ForceBaseBuild),
				FromImage: new(tc.fromImage),
				Steps: new([]api.TemplateStep{
					{
						Type:  "USER",
						Force: new(true),
						Args:  new([]string{"testuser", "true"}),
					},
					{
						Type: "RUN",
						Args: new([]string{"whoami && id testuser"}),
					},
				}),
			}, defaultBuildLogHandler(t)))
		})
	}
}
