package featureflags

import (
	"testing"

	"github.com/stretchr/testify/require"
)

//nolint:paralleltest,tparallel // t.Setenv
func TestEnvIntOr(t *testing.T) {
	const key = "PAUSE_ADMISSION_DISK_HEADROOM_MIB_TEST_ONLY"

	for _, tc := range []struct {
		name     string
		set      bool
		value    string
		fallback int
		want     int
	}{
		{name: "unset keeps the fallback", fallback: -1, want: -1},
		{name: "empty keeps the fallback", set: true, value: "", fallback: -1, want: -1},
		{name: "a number overrides the fallback", set: true, value: "1024", fallback: -1, want: 1024},
		{name: "zero is a value", set: true, value: "0", fallback: -1, want: 0},
		{name: "negative turns a check off", set: true, value: "-1", fallback: 1024, want: -1},
		// An unparseable value must not silently read as zero.
		{name: "garbage keeps the fallback", set: true, value: "lots", fallback: -1, want: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			}

			require.Equal(t, tc.want, envIntOr(key, tc.fallback))
		})
	}
}

//nolint:paralleltest,tparallel // t.Setenv
func TestEnvPositiveIntOr(t *testing.T) {
	const key = "MAX_SANDBOXES_PER_NODE_TEST_ONLY"

	for _, tc := range []struct {
		name     string
		set      bool
		value    string
		fallback int
		want     int
	}{
		{name: "unset keeps the fallback", fallback: 200, want: 200},
		{name: "empty keeps the fallback", set: true, value: "", fallback: 200, want: 200},
		{name: "a value overrides the fallback", set: true, value: "1200", fallback: 200, want: 1200},
		{name: "garbage keeps the fallback", set: true, value: "many", fallback: 200, want: 200},
		{name: "zero keeps the fallback", set: true, value: "0", fallback: 200, want: 200},
		{name: "negative keeps the fallback", set: true, value: "-1", fallback: 200, want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			}

			require.Equal(t, tc.want, envPositiveIntOr(key, tc.fallback))
		})
	}
}
