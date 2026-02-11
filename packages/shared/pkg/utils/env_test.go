package utils

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTargetArch_DefaultsToRuntimeGOARCH(t *testing.T) {
	t.Setenv("TARGET_ARCH", "")

	result := TargetArch()

	assert.Equal(t, runtime.GOARCH, result)
}

func TestTargetArch_RespectsValidOverride(t *testing.T) {
	tests := []struct {
		name string
		arch string
	}{
		{name: "amd64", arch: "amd64"},
		{name: "arm64", arch: "arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TARGET_ARCH", tt.arch)

			result := TargetArch()

			assert.Equal(t, tt.arch, result)
		})
	}
}

func TestTargetArch_PanicsOnInvalidValue(t *testing.T) {
	tests := []struct {
		name string
		arch string
	}{
		{name: "x86_64", arch: "x86_64"},
		{name: "aarch64", arch: "aarch64"},
		{name: "mips", arch: "mips"},
		{name: "386", arch: "386"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TARGET_ARCH", tt.arch)

			assert.Panics(t, func() {
				TargetArch()
			})
		})
	}
}

func TestTargetArch_UnsetFallsThrough(t *testing.T) {
	// Ensure TARGET_ARCH is completely unset, not just empty
	t.Setenv("TARGET_ARCH", "")

	result := TargetArch()

	assert.Contains(t, []string{"amd64", "arm64"}, result)
}
