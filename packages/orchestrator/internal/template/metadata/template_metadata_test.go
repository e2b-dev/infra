package metadata

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestDeserialize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		input          string
		expectedResult Template
		expectedError  string
	}{
		{
			name:  "Valid current version template with all fields",
			input: `{"version": 2, "template": {"build_id": "build123", "kernel_version": "5.10", "firecracker_version": "1.0"}, "context": {"user": "testuser", "workdir": "/app", "env_vars": {"KEY": "value"}}, "start": {"start_command": "npm start", "ready_command": "echo ready", "context": {"user": "root"}}, "from_image": "ubuntu:20.04"}`,
			expectedResult: Template{
				Version: 2,
				Template: TemplateMetadata{
					BuildID:            "build123",
					KernelVersion:      "5.10",
					FirecrackerVersion: "1.0",
				},
				Context: Context{
					User:    "testuser",
					WorkDir: utils.ToPtr("/app"),
					EnvVars: map[string]string{"KEY": "value"},
				},
				Start: &Start{
					StartCmd: "npm start",
					ReadyCmd: "echo ready",
					Context: Context{
						User: "root",
					},
				},
				FromImage: utils.ToPtr("ubuntu:20.04"),
			},
		},
		{
			name:  "Valid current version template with from_template",
			input: `{"version": 2, "template": {"build_id": "build456", "kernel_version": "5.10", "firecracker_version": "1.0"}, "context": {"user": "testuser"}, "from_template": {"alias": "base-template", "build_id": "base-build-123"}}`,
			expectedResult: Template{
				Version: 2,
				Template: TemplateMetadata{
					BuildID:            "build456",
					KernelVersion:      "5.10",
					FirecrackerVersion: "1.0",
				},
				Context: Context{
					User: "testuser",
				},
				FromTemplate: &FromTemplate{
					Alias:   "base-template",
					BuildID: "base-build-123",
				},
			},
		},
		{
			name:  "Valid current version template minimal fields",
			input: `{"version": 2, "template": {"build_id": "build789", "kernel_version": "5.10", "firecracker_version": "1.0"}, "context": {}}`,
			expectedResult: Template{
				Version: 2,
				Template: TemplateMetadata{
					BuildID:            "build789",
					KernelVersion:      "5.10",
					FirecrackerVersion: "1.0",
				},
				Context: Context{},
			},
		},
		{
			name:  "Deprecated version 1",
			input: `{"version": 1, "some": "data"}`,
			expectedResult: Template{
				Version: DeprecatedVersion,
			},
		},
		{
			name:  "Version less than deprecated (0)",
			input: `{"version": 0, "some": "data"}`,
			expectedResult: Template{
				Version: DeprecatedVersion,
			},
		},
		{
			name:  "Version as string (should be treated as deprecated)",
			input: `{"version": "1", "some": "data"}`,
			expectedResult: Template{
				Version: DeprecatedVersion,
			},
		},
		{
			name:  "No version field",
			input: `{"some": "data"}`,
			expectedResult: Template{
				Version: DeprecatedVersion,
			},
		},
		{
			name:          "Invalid JSON",
			input:         `{"version": 2, "template": {invalid json`,
			expectedError: "error unmarshaling template version",
		},
		{
			name:          "Empty input",
			input:         "",
			expectedError: "error unmarshaling template version",
		},
		{
			name:          "Valid version but invalid template structure",
			input:         `{"version": 2, "template": "invalid_template_structure"}`,
			expectedError: "error unmarshaling template metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := strings.NewReader(tt.input)
			result, err := deserialize(reader)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedResult.Version, result.Version)
			assert.Equal(t, tt.expectedResult.Template.BuildID, result.Template.BuildID)
			assert.Equal(t, tt.expectedResult.Template.KernelVersion, result.Template.KernelVersion)
			assert.Equal(t, tt.expectedResult.Template.FirecrackerVersion, result.Template.FirecrackerVersion)
			assert.Equal(t, tt.expectedResult.Context.User, result.Context.User)
			assert.Equal(t, tt.expectedResult.Context.WorkDir, result.Context.WorkDir)
			assert.Equal(t, tt.expectedResult.Context.EnvVars, result.Context.EnvVars)
			assert.Equal(t, tt.expectedResult.Start, result.Start)
			assert.Equal(t, tt.expectedResult.FromImage, result.FromImage)
			assert.Equal(t, tt.expectedResult.FromTemplate, result.FromTemplate)
		})
	}
}

func TestDeserialize_ReadError(t *testing.T) {
	t.Parallel()
	// Create a reader that always returns an error
	errorReader := &errorReader{err: io.ErrUnexpectedEOF}

	_, err := deserialize(errorReader)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "error reading template metadata")
}

func TestDeserialize_VersionEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		input           string
		expectedVersion uint64
	}{
		{
			name:            "Version exactly equals deprecated version",
			input:           `{"version": 1}`,
			expectedVersion: DeprecatedVersion,
		},
		{
			name:            "Version as float 1.0",
			input:           `{"version": 1.0}`,
			expectedVersion: DeprecatedVersion,
		},

		{
			name:            "Version as negative number",
			input:           `{"version": -1}`,
			expectedVersion: DeprecatedVersion,
		},
		{
			name:            "Version as null",
			input:           `{"version": null}`,
			expectedVersion: DeprecatedVersion,
		},
		{
			name:            "Version as boolean",
			input:           `{"version": true}`,
			expectedVersion: DeprecatedVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := strings.NewReader(tt.input)
			result, err := deserialize(reader)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedVersion, result.Version)
		})
	}
}

// errorReader is a test helper that always returns an error when read
type errorReader struct {
	err error
}

func (er *errorReader) Read([]byte) (n int, err error) {
	return 0, er.err
}
