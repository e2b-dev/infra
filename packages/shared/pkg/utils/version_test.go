package utils

import (
	"testing"
)

func TestIsGTEVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		curVersion  string
		minVersion  string
		expected    bool
		expectError bool
	}{
		// Valid version comparisons
		{
			name:        "current version greater than minimum",
			curVersion:  "1.2.3",
			minVersion:  "1.2.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "current version equal to minimum",
			curVersion:  "1.2.3",
			minVersion:  "1.2.3",
			expected:    true,
			expectError: false,
		},
		{
			name:        "current version less than minimum",
			curVersion:  "1.2.0",
			minVersion:  "1.2.3",
			expected:    false,
			expectError: false,
		},
		{
			name:        "versions with v prefix",
			curVersion:  "v1.2.3",
			minVersion:  "v1.2.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "mixed v prefix",
			curVersion:  "v1.2.3",
			minVersion:  "1.2.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "mixed v prefix reverse",
			curVersion:  "1.2.3",
			minVersion:  "v1.2.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "pre-release versions",
			curVersion:  "1.2.3-alpha",
			minVersion:  "1.2.3",
			expected:    false,
			expectError: false,
		},
		{
			name:        "pre-release comparison",
			curVersion:  "1.2.3-beta",
			minVersion:  "1.2.3-alpha",
			expected:    true,
			expectError: false,
		},
		{
			name:        "build metadata",
			curVersion:  "1.2.3+build.1",
			minVersion:  "1.2.3",
			expected:    true,
			expectError: false,
		},
		{
			name:        "zero versions",
			curVersion:  "0.0.0",
			minVersion:  "0.0.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "current version missing patch",
			curVersion:  "1.2",
			minVersion:  "1.0.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "current version missing minor",
			curVersion:  "1",
			minVersion:  "1.0.0",
			expected:    true,
			expectError: false,
		},
		{
			name:        "minimum version missing minor",
			curVersion:  "1.0.0",
			minVersion:  "2",
			expected:    false,
			expectError: false,
		},
		{
			name:        "empty current version",
			curVersion:  "",
			minVersion:  "1.2.0",
			expected:    false,
			expectError: true,
		},
		{
			name:        "empty minimum version",
			curVersion:  "1.2.0",
			minVersion:  "",
			expected:    false,
			expectError: true,
		},
		{
			name:        "both versions empty",
			curVersion:  "",
			minVersion:  "",
			expected:    false,
			expectError: true,
		},
		{
			name:        "invalid format with extra dots",
			curVersion:  "1.2.3.4",
			minVersion:  "1.2.0",
			expected:    false,
			expectError: true,
		},
		{
			name:        "invalid format with spaces",
			curVersion:  "1.2.3",
			minVersion:  "1.2 .0",
			expected:    false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := IsGTEVersion(tt.curVersion, tt.minVersion)

			if tt.expectError {
				if err == nil {
					t.Errorf("IsGTEVersion() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("IsGTEVersion() unexpected error: %v", err)
				}
			}

			if result != tt.expected {
				t.Errorf("IsGTEVersion() = %v, want %v", result, tt.expected)
			}
		})
	}
}
