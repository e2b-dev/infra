package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
)

// TestEnvdWorkdirWithheld pins what "withheld" means, because the live-upgrade gate declines
// a swap on it and the two terms are not interchangeable.
//
// The second term is the one that matters and the one easy to drop: a filesystem-only cold
// boot reconstructs and SENDS the recorded workdir, and it reaches the same gate as a memory
// resume. Treating "recorded" alone as withheld would decline those swaps for a value that
// was never at risk.
func TestEnvdWorkdirWithheld(t *testing.T) {
	t.Parallel()

	wd := "/opt/wd"
	other := "/srv"

	for name, tc := range map[string]struct {
		recorded *string
		sent     *string
		want     bool
	}{
		"recorded and not sent is withheld":  {recorded: &wd, sent: nil, want: true},
		"recorded and sent is not withheld":  {recorded: &wd, sent: &wd, want: false},
		"recorded and a different one sent":  {recorded: &wd, sent: &other, want: false},
		"nothing recorded is never withheld": {recorded: nil, sent: nil, want: false},
		"nothing recorded but one sent":      {recorded: nil, sent: &wd, want: false},
		"recorded empty string still counts": {recorded: new(""), sent: nil, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			meta := metadata.Template{Context: metadata.Context{WorkDir: tc.recorded}}
			assert.Equal(t, tc.want, envdWorkdirWithheld(meta, tc.sent))
		})
	}
}
