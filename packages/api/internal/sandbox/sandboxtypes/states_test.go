package sandboxtypes

import "testing"

func TestKillReasonValues(t *testing.T) {
	t.Parallel()

	for reason, want := range map[KillReason]string{
		KillReasonUnknown:             "unknown",
		KillReasonRequest:             "request",
		KillReasonTimeout:             "timeout",
		KillReasonAdmin:               "admin",
		KillReasonOrphaned:            "orphaned",
		KillReasonBaseTemplateMissing: "base_template_missing",
	} {
		if string(reason) != want {
			t.Errorf("KillReason = %q, want %q", string(reason), want)
		}
	}
}
