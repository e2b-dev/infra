package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

// TestDefaultsHeader_WireFormat pins the exact bytes of X-Envd-Defaults. The orchestrator
// decodes this header from its own tree on its own release cadence, so the field names are a
// contract between two independently deployed components: rename one here and the mismatch
// signal goes quiet with nothing to indicate it.
func TestDefaultsHeader_WireFormat(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(effectiveDefaults{User: "user", Workdir: new("/opt/wd"), Fallback: false})
	require.NoError(t, err)
	assert.JSONEq(t, `{"user":"user","workdir":"/opt/wd","fallback":false}`, string(b))

	// fallback=false must be PRESENT, not omitted: it is the central result -- the statement
	// that an identity was actually delivered -- and a reader cannot tell an omitted field
	// from an envd too old to have it.
	b, err = json.Marshal(effectiveDefaults{User: "root", Fallback: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"user":"root","fallback":true}`, string(b))
}

func apiWithDefaults(user string, delivered bool, workdir *string, handover *handoverResult, buf *bytes.Buffer) *API {
	l := zerolog.New(buf)

	return &API{
		defaults: &execcontext.Defaults{
			User: user, UserDelivered: delivered, Workdir: workdir, EnvVars: utils.NewEnvVars(),
		},
		handover: handover,
		logger:   &l,
	}
}

// TestReportEffectiveDefaults covers what the header says and, crucially, WHEN the warn fires.
// The warn is the in-guest half of the signal, and it is scoped to a handover boot on purpose:
// on a cold boot the fallback is legitimate and momentary, so warning there would be noise that
// trains people to ignore it.
func TestReportEffectiveDefaults(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		user      string
		delivered bool
		workdir   *string
		handover  *handoverResult
		wantUser  string
		wantFall  bool
		wantWarn  bool
	}{
		{
			name: "delivered identity on a handover boot: no warn",
			user: "user", delivered: true, handover: &handoverResult{},
			wantUser: "user", wantFall: false, wantWarn: false,
		},
		{
			name: "never told, on a handover boot: this is the loss, so warn",
			user: execcontext.BuiltinDefaultUser, delivered: false, handover: &handoverResult{},
			wantUser: execcontext.BuiltinDefaultUser, wantFall: true, wantWarn: true,
		},
		{
			name: "never told WITHOUT a handover is a normal cold boot: no warn",
			user: execcontext.BuiltinDefaultUser, delivered: false, handover: nil,
			wantUser: execcontext.BuiltinDefaultUser, wantFall: true, wantWarn: false,
		},
		{
			// The case value equality gets wrong: this identity WAS delivered, it just
			// happens to equal the compiled-in default. Reporting it as a fallback would
			// cry wolf on every resume for the rest of the sandbox's life, because the
			// handover result is never cleared.
			name: "a template whose user genuinely IS the builtin default is not a fallback",
			user: execcontext.BuiltinDefaultUser, delivered: true, handover: &handoverResult{},
			wantUser: execcontext.BuiltinDefaultUser, wantFall: false, wantWarn: false,
		},
		{
			name: "workdir is carried through",
			user: "user", delivered: true, workdir: new("/opt/wd"), handover: &handoverResult{},
			wantUser: "user", wantFall: false, wantWarn: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			a := apiWithDefaults(tc.user, tc.delivered, tc.workdir, tc.handover, &buf)
			w := httptest.NewRecorder()
			a.reportEffectiveDefaults(w, *a.logger)

			raw := w.Header().Get(defaultsHeader)
			require.NotEmpty(t, raw, "the header must always be set")

			var got effectiveDefaults
			require.NoError(t, json.Unmarshal([]byte(raw), &got))
			assert.Equal(t, tc.wantUser, got.User)
			assert.Equal(t, tc.wantFall, got.Fallback)
			if tc.workdir == nil {
				assert.Nil(t, got.Workdir)
			} else {
				require.NotNil(t, got.Workdir)
				assert.Equal(t, *tc.workdir, *got.Workdir)
			}

			warned := bytes.Contains(buf.Bytes(), []byte("never told which user to run as"))
			assert.Equal(t, tc.wantWarn, warned, "warn presence; log was: %s", buf.String())
		})
	}
}
