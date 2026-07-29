package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFilteredEnvironmentRemovesCredentialInputs(t *testing.T) {
	t.Parallel()

	got := filteredEnvironment([]string{
		"PATH=/usr/bin",
		"POSTGRES_CONNECTION_STRING=postgresql://sensitive",
		"E2B_API_KEY=e2b_sensitive",
		"E2B_ACCESS_TOKEN=access-sensitive",
		"SAFE_VALUE=retained",
	})

	want := []string{"PATH=/usr/bin", "SAFE_VALUE=retained"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("filtered environment mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestAddSecretVersionUsesStdinAndFiltersCredentials(t *testing.T) {
	testDir := t.TempDir()
	argsPath := filepath.Join(testDir, "args")
	envPath := filepath.Join(testDir, "env")
	stdinPath := filepath.Join(testDir, "stdin")
	fakeGcloud := filepath.Join(testDir, "gcloud")

	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" >"$CANARY_TEST_ARGS"
env >"$CANARY_TEST_ENV"
cat >"$CANARY_TEST_STDIN"
printf 'projects/operator-canary/secrets/canary/versions/1\n'
`
	if err := os.WriteFile(fakeGcloud, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}

	t.Setenv("CANARY_TEST_ARGS", argsPath)
	t.Setenv("CANARY_TEST_ENV", envPath)
	t.Setenv("CANARY_TEST_STDIN", stdinPath)
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgresql://database-sensitive")
	t.Setenv("E2B_API_KEY", "environment-api-sensitive")
	t.Setenv("E2B_ACCESS_TOKEN", "environment-access-sensitive")

	const rawAPIKey = "e2b_raw_stdin_only"
	version, err := addSecretVersion(
		context.Background(),
		config{
			gcloud:  fakeGcloud,
			project: "operator-canary",
		},
		"canary-secret",
		rawAPIKey,
	)
	if err != nil {
		t.Fatalf("add secret version: %v", err)
	}
	if version != "projects/operator-canary/secrets/canary/versions/1" {
		t.Fatalf("unexpected secret version: %q", version)
	}

	args := readTestFile(t, argsPath)
	if !strings.Contains(args, "--data-file=-") {
		t.Fatalf("gcloud arguments do not require stdin: %q", args)
	}
	if strings.Contains(args, rawAPIKey) {
		t.Fatal("raw API key appeared in command arguments")
	}

	environment := readTestFile(t, envPath)
	for _, sensitive := range []string{
		"database-sensitive",
		"environment-api-sensitive",
		"environment-access-sensitive",
		rawAPIKey,
	} {
		if strings.Contains(environment, sensitive) {
			t.Fatalf("sensitive value %q appeared in child environment", sensitive)
		}
	}

	if stdin := readTestFile(t, stdinPath); stdin != rawAPIKey {
		t.Fatalf("raw API key was not delivered as exact stdin bytes: %q", stdin)
	}
}

func TestStateFileIsPrivateExclusiveAndReplaceable(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "canary-state.json")
	initial := bootstrapState{
		Version:  stateVersion,
		Project:  "operator-canary",
		SecretID: "canary-secret-20260729t120000z-deadbeef",
		Suffix:   "20260729t120000z-deadbeef",
		TeamID:   uuid.New(),
	}
	if err := createStateFile(statePath, initial); err != nil {
		t.Fatalf("create state: %v", err)
	}
	if err := createStateFile(statePath, initial); err == nil {
		t.Fatal("expected an existing state file to block a second canary")
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state permissions = %o, want 600", got)
	}

	apiKeyID := uuid.New()
	updated := initial
	updated.APIKeyID = &apiKeyID
	if err := replaceStateFile(statePath, updated); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	got, err := readStateFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if got.TeamID != initial.TeamID || got.APIKeyID == nil || *got.APIKeyID != apiKeyID {
		t.Fatalf("state round trip mismatch: %#v", got)
	}

	if err := os.Chmod(statePath, 0o644); err != nil {
		t.Fatalf("weaken state permissions: %v", err)
	}
	if _, err := readStateFile(statePath); err == nil {
		t.Fatal("expected non-private state permissions to be rejected")
	}
}

func TestInspectCanarySecretTreatsOnlyNotFoundAsAbsent(t *testing.T) {
	testDir := t.TempDir()
	fakeGcloud := filepath.Join(testDir, "gcloud")
	if err := os.WriteFile(fakeGcloud, []byte("#!/bin/sh\nprintf 'NOT_FOUND\\n' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}

	state := bootstrapState{
		SecretID: "canary-secret-20260729t120000z-deadbeef",
	}
	exists, err := inspectCanarySecret(
		context.Background(),
		config{gcloud: fakeGcloud, project: "operator-canary"},
		state,
	)
	if err != nil {
		t.Fatalf("inspect absent secret: %v", err)
	}
	if exists {
		t.Fatal("not-found secret reported as existing")
	}

	if err := os.WriteFile(fakeGcloud, []byte("#!/bin/sh\nprintf 'PERMISSION_DENIED\\n' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("rewrite fake gcloud: %v", err)
	}
	if _, err := inspectCanarySecret(
		context.Background(),
		config{gcloud: fakeGcloud, project: "operator-canary"},
		state,
	); err == nil {
		t.Fatal("expected a non-not-found gcloud failure to be preserved")
	}
}

func TestInspectCanarySecretRequiresOwnedLabelAndName(t *testing.T) {
	testDir := t.TempDir()
	fakeGcloud := filepath.Join(testDir, "gcloud")
	state := bootstrapState{
		SecretID: "canary-secret-20260729t120000z-deadbeef",
	}

	writeFakeGcloud := func(output string) {
		t.Helper()
		script := "#!/bin/sh\nprintf '%s' '" + output + "'\n"
		if err := os.WriteFile(fakeGcloud, []byte(script), 0o700); err != nil {
			t.Fatalf("write fake gcloud: %v", err)
		}
	}

	writeFakeGcloud(`{"name":"projects/1/secrets/canary-secret-20260729t120000z-deadbeef","labels":{"purpose":"other"}}`)
	if _, err := inspectCanarySecret(
		context.Background(),
		config{gcloud: fakeGcloud, project: "operator-canary"},
		state,
	); err == nil {
		t.Fatal("expected a non-canary secret label to be rejected")
	}

	writeFakeGcloud(`{"name":"projects/1/secrets/another-secret","labels":{"purpose":"monad-sdk-canary"}}`)
	if _, err := inspectCanarySecret(
		context.Background(),
		config{gcloud: fakeGcloud, project: "operator-canary"},
		state,
	); err == nil {
		t.Fatal("expected a mismatched secret name to be rejected")
	}

	writeFakeGcloud(`{"name":"projects/1/secrets/canary-secret-20260729t120000z-deadbeef","labels":{"purpose":"monad-sdk-canary"}}`)
	exists, err := inspectCanarySecret(
		context.Background(),
		config{gcloud: fakeGcloud, project: "operator-canary"},
		state,
	)
	if err != nil || !exists {
		t.Fatalf("expected owned canary secret, exists=%v err=%v", exists, err)
	}
}

func TestCanaryTeamIdentityMatchesExactSuffix(t *testing.T) {
	const suffix = "20260729t120000z-deadbeef"
	if !canaryTeamIdentityMatches(
		suffix,
		"monad-sdk-canary+"+suffix+"@example.invalid",
		"Monad SDK canary "+suffix,
		"monad-sdk-canary-"+suffix,
	) {
		t.Fatal("expected exact generated team identity to match")
	}
	if canaryTeamIdentityMatches(
		suffix,
		"real-customer@example.com",
		"Monad SDK canary "+suffix,
		"monad-sdk-canary-"+suffix,
	) {
		t.Fatal("expected a non-canary team identity to be rejected")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
