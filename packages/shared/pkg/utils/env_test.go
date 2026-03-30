package utils

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTargetArch_DefaultsToHostArch(t *testing.T) {
	t.Setenv("TARGET_ARCH", "")

	result := TargetArch()

	assert.Equal(t, runtime.GOARCH, result)
}

func TestTargetArch_RespectsValidOverride(t *testing.T) {
	tests := []struct {
		name     string
		arch     string
		expected string
	}{
		{name: "amd64", arch: "amd64", expected: "amd64"},
		{name: "arm64", arch: "arm64", expected: "arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TARGET_ARCH", tt.arch)

			result := TargetArch()

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTargetArch_NormalizesAliases(t *testing.T) {
	tests := []struct {
		name     string
		arch     string
		expected string
	}{
		{name: "x86_64 → amd64", arch: "x86_64", expected: "amd64"},
		{name: "aarch64 → arm64", arch: "aarch64", expected: "arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TARGET_ARCH", tt.arch)

			result := TargetArch()

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTargetArch_FallsBackOnUnknown(t *testing.T) {
	t.Setenv("TARGET_ARCH", "mips")

	result := TargetArch()

	assert.Equal(t, runtime.GOARCH, result)
}
