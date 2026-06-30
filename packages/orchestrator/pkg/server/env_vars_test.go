//go:build linux

package server

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mergeTemplateEnvVars mirrors the inline merge logic in Create() and
// RebootSandbox(): template (Docker image) env vars form the base layer,
// user-provided env vars override on top.
func mergeTemplateEnvVars(templateEnvVars, userEnvVars map[string]string) map[string]string {
	if len(templateEnvVars) == 0 {
		return userEnvVars
	}

	merged := maps.Clone(templateEnvVars)
	maps.Copy(merged, userEnvVars)

	return merged
}

func TestMergeTemplateEnvVars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		templateEnv map[string]string
		userEnv     map[string]string
		want        map[string]string
	}{
		{
			name:        "no template env, no user env",
			templateEnv: nil,
			userEnv:     nil,
			want:        nil,
		},
		{
			name:        "no template env, user env only",
			templateEnv: nil,
			userEnv:     map[string]string{"USER_KEY": "user_val"},
			want:        map[string]string{"USER_KEY": "user_val"},
		},
		{
			name:        "template env only, no user env",
			templateEnv: map[string]string{"PYTHONPATH": "/app/lib", "MY_CONFIG": "/etc/config"},
			userEnv:     nil,
			want:        map[string]string{"PYTHONPATH": "/app/lib", "MY_CONFIG": "/etc/config"},
		},
		{
			name:        "disjoint keys are merged",
			templateEnv: map[string]string{"PYTHONPATH": "/app/lib"},
			userEnv:     map[string]string{"API_KEY": "secret"},
			want:        map[string]string{"PYTHONPATH": "/app/lib", "API_KEY": "secret"},
		},
		{
			name:        "user env overrides template env on conflict",
			templateEnv: map[string]string{"PYTHONPATH": "/app/lib", "LOG_LEVEL": "info"},
			userEnv:     map[string]string{"LOG_LEVEL": "debug", "NEW_VAR": "value"},
			want:        map[string]string{"PYTHONPATH": "/app/lib", "LOG_LEVEL": "debug", "NEW_VAR": "value"},
		},
		{
			name:        "user env completely overrides all template keys",
			templateEnv: map[string]string{"A": "1", "B": "2"},
			userEnv:     map[string]string{"A": "x", "B": "y"},
			want:        map[string]string{"A": "x", "B": "y"},
		},
		{
			name:        "empty template env map treated as no template env",
			templateEnv: map[string]string{},
			userEnv:     map[string]string{"KEY": "val"},
			want:        map[string]string{"KEY": "val"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mergeTemplateEnvVars(tt.templateEnv, tt.userEnv)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeTemplateEnvVars_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	templateEnv := map[string]string{"A": "1", "B": "2"}
	userEnv := map[string]string{"B": "override", "C": "3"}

	templateCopy := maps.Clone(templateEnv)
	userCopy := maps.Clone(userEnv)

	_ = mergeTemplateEnvVars(templateEnv, userEnv)

	assert.Equal(t, templateCopy, templateEnv, "template env should not be mutated")
	assert.Equal(t, userCopy, userEnv, "user env should not be mutated")
}
