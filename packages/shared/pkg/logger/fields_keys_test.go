package logger

import (
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Locks the canonical structured-log field keys. If a helper's key changes,
// the Vector pipeline remap (packages/local-dev/vector.toml) and every log
// query/alert that matches these keys must change with it — so a silent edit
// here should fail loudly.
func TestCanonicalFieldKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		field   zap.Field
		wantKey string
	}{
		{"sandbox", WithSandboxID("x"), KeySandboxID},
		{"lifecycle", WithLifecycleID("x"), KeyLifecycleID},
		{"template", WithTemplateID("x"), KeyTemplateID},
		{"build", WithBuildID("x"), KeyBuildID},
		{"execution", WithExecutionID("x"), KeyExecutionID},
		{"user", WithUserID("x"), KeyUserID},
		{"team", WithTeamID("x"), KeyTeamID},
		{"node", WithNodeID("x"), KeyNodeID},
		{"cluster", WithClusterID(uuid.Nil), KeyClusterID},
		{"edgeTrace", WithEdgeTraceID("x"), KeyEdgeTraceID},
		{"serviceInstance", WithServiceInstanceID("x"), KeyServiceInstance},
		{"sandboxIP", WithSandboxIP("x"), KeySandboxIP},
	}

	// Guard against duplicate key strings across the canonical set.
	seen := map[string]string{}

	for _, tt := range tests {
		if tt.field.Key != tt.wantKey {
			t.Errorf("%s: key = %q, want %q", tt.name, tt.field.Key, tt.wantKey)
		}

		if prev, ok := seen[tt.wantKey]; ok {
			t.Errorf("duplicate canonical key %q used by both %q and %q", tt.wantKey, prev, tt.name)
		}

		seen[tt.wantKey] = tt.name
	}

	// Every key uses the dotted "<noun>.id"/"<noun>.ip" convention, not
	// snake_case or camelCase, so queries can rely on a single spelling.
	for key := range seen {
		for _, c := range key {
			if c >= 'A' && c <= 'Z' {
				t.Errorf("canonical key %q must be lowercase dotted, found uppercase", key)
			}

			if c == '_' {
				t.Errorf("canonical key %q must use dots, not underscores", key)
			}
		}
	}
}
