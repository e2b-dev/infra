package storageopts

import "testing"

func TestParseSoftDeleteMarker(t *testing.T) {
	t.Parallel()

	cases := []struct {
		marker     string
		wantReason string
		wantAction string
	}{
		{"orphaned:abc123", "orphaned", "abc123"},
		{"orphaned", "orphaned", ""},
		{"", "", ""},
		{":abc123", "", "abc123"},
		{"reason:with:colons", "reason", "with:colons"},
	}

	for _, c := range cases {
		reason, action := ParseSoftDeleteMarker(c.marker)
		if reason != c.wantReason || action != c.wantAction {
			t.Errorf("ParseSoftDeleteMarker(%q) = (%q, %q), want (%q, %q)", c.marker, reason, action, c.wantReason, c.wantAction)
		}
	}
}

func TestSoftDeleteReasonGroup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		reason string
		want   string
	}{
		{"orphaned", "orphaned"},
		{"user_delete", "user_delete"},
		{"team-expired", "team-expired"},
		{"", SoftDeleteReasonOther},
		{"Has Spaces", SoftDeleteReasonOther},
		{"UPPER", SoftDeleteReasonOther},
		{"way-too-long-reason-value-that-exceeds-the-cap", SoftDeleteReasonOther},
	}

	for _, c := range cases {
		if got := SoftDeleteReasonGroup(c.reason); got != c.want {
			t.Errorf("SoftDeleteReasonGroup(%q) = %q, want %q", c.reason, got, c.want)
		}
	}
}
