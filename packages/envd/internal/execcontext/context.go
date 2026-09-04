package execcontext

import (
	"errors"

	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

// BuiltinDefaultUser is the user envd serves as when nothing has told it otherwise.
// It is the compiled-in fallback, not a policy: a template's own user arrives via
// /init (and, across a live upgrade, via the handover blob), and every start path is
// expected to supply one.
//
// It lives here rather than in main so the API layer can recognise "still on the
// fallback" — which, on a handover boot, is an identity that was lost rather than a
// default that was chosen.
const BuiltinDefaultUser = "root"

type Defaults struct {
	EnvVars *utils.EnvVars
	User    string
	Workdir *string

	// UserDelivered records that something actually told this envd who to run as — an
	// /init carrying a user, or a handover blob. It is provenance, not a value test:
	// a template whose default user genuinely IS BuiltinDefaultUser is indistinguishable
	// from one that was never told, if you only compare strings. Those two need opposite
	// treatment, so the fact is recorded when it happens instead of being inferred later.
	UserDelivered bool
}

func ResolveDefaultWorkdir(workdir string, defaultWorkdir *string) string {
	if workdir != "" {
		return workdir
	}

	if defaultWorkdir != nil {
		return *defaultWorkdir
	}

	return ""
}

func ResolveDefaultUsername(username *string, defaultUsername string) (string, error) {
	if username != nil {
		return *username, nil
	}

	if defaultUsername != "" {
		return defaultUsername, nil
	}

	return "", errors.New("username not provided")
}
