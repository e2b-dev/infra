package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

const (
	defaultSecretPrefix = "e2b-sdk-canary-api-key"
	defaultTier         = "base_v1"
	stateVersion        = 2
)

var secretIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var canarySuffixPattern = regexp.MustCompile(`^[0-9]{8}t[0-9]{6}z-[a-f0-9]{8}$`)

type config struct {
	databaseURL  string
	gcloud       string
	project      string
	secretPrefix string
	statePath    string
	tier         string
	cleanup      bool
}

type bootstrapState struct {
	Version  int        `json:"version"`
	Project  string     `json:"project"`
	SecretID string     `json:"secret_id"`
	Suffix   string     `json:"suffix"`
	TeamID   uuid.UUID  `json:"team_id"`
	APIKeyID *uuid.UUID `json:"api_key_id,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := parseConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "canary bootstrap refused: %v\n", err)
		os.Exit(2)
	}

	operation := bootstrap
	operationName := "bootstrap"
	if cfg.cleanup {
		operation = cleanup
		operationName = "cleanup"
	}

	if err := operation(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "canary %s failed: %v\n", operationName, err)
		os.Exit(1)
	}
}

func parseConfig() (config, error) {
	var cfg config

	flag.StringVar(&cfg.project, "project", os.Getenv("GCP_PROJECT_ID"), "GCP project that will own the canary secret")
	flag.StringVar(&cfg.secretPrefix, "secret-prefix", defaultSecretPrefix, "prefix for the unique Secret Manager secret")
	flag.StringVar(&cfg.tier, "tier", defaultTier, "existing database tier for the canary team")
	flag.StringVar(&cfg.gcloud, "gcloud", "gcloud", "path to the gcloud executable")
	flag.StringVar(&cfg.statePath, "state-file", "", "private reconciliation file for the canary identifiers")
	flag.BoolVar(&cfg.cleanup, "cleanup", false, "delete the persisted canary API key, team, secret, and state file")
	flag.Parse()

	cfg.databaseURL = strings.TrimSpace(os.Getenv("POSTGRES_CONNECTION_STRING"))
	cfg.project = strings.TrimSpace(cfg.project)
	cfg.secretPrefix = strings.TrimSpace(cfg.secretPrefix)
	cfg.statePath = strings.TrimSpace(cfg.statePath)
	cfg.tier = strings.TrimSpace(cfg.tier)

	switch {
	case cfg.databaseURL == "":
		return config{}, errors.New("POSTGRES_CONNECTION_STRING is required")
	case !cfg.cleanup && cfg.project == "":
		return config{}, errors.New("--project or GCP_PROJECT_ID is required")
	case cfg.statePath == "":
		return config{}, errors.New("--state-file is required for bootstrap and cleanup")
	case cfg.secretPrefix == "":
		return config{}, errors.New("--secret-prefix cannot be empty")
	case !secretIDPattern.MatchString(cfg.secretPrefix):
		return config{}, errors.New("--secret-prefix may contain only letters, numbers, hyphens, and underscores")
	case cfg.tier == "":
		return config{}, errors.New("--tier cannot be empty")
	}
	cfg.statePath = filepath.Clean(cfg.statePath)

	gcloudPath, err := exec.LookPath(cfg.gcloud)
	if err != nil {
		return config{}, errors.New("gcloud executable was not found")
	}
	cfg.gcloud = gcloudPath

	return cfg, nil
}

func bootstrap(ctx context.Context, cfg config) error {
	suffix := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102t150405z"), uuid.NewString()[:8])
	secretID := fmt.Sprintf("%s-%s", cfg.secretPrefix, suffix)
	if len(secretID) > 255 {
		return errors.New("generated Secret Manager secret ID exceeds 255 characters")
	}

	state := bootstrapState{
		Version:  stateVersion,
		Project:  cfg.project,
		SecretID: secretID,
		Suffix:   suffix,
		TeamID:   uuid.New(),
	}
	if err := createStateFile(cfg.statePath, state); err != nil {
		return err
	}

	keepState := false
	defer func() {
		if keepState {
			return
		}

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := reconcile(cleanupCtx, cfg, state); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"cleanup required: run with --cleanup --state-file %q\n",
				cfg.statePath,
			)
			return
		}
		if err := removeStateFile(cfg.statePath); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"cleanup completed but state removal failed: remove %q after verifying the identifiers\n",
				cfg.statePath,
			)
		}
	}()

	if err := createSecret(ctx, cfg, secretID); err != nil {
		return err
	}

	db, err := authdb.NewClient(ctx, cfg.databaseURL)
	if err != nil {
		return errors.New("could not connect to the canary database")
	}
	defer db.Close()

	queries, tx, err := db.WithTx(ctx)
	if err != nil {
		return errors.New("could not start the canary database transaction")
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	teamSlug := "monad-sdk-canary-" + suffix
	teamEmail := "monad-sdk-canary+" + suffix + "@example.invalid"

	_, err = tx.Exec(ctx, `
INSERT INTO public.teams (id, email, name, tier, is_blocked, slug)
VALUES ($1, $2, $3, $4, FALSE, $5)
`, state.TeamID, teamEmail, "Monad SDK canary "+suffix, cfg.tier, teamSlug)
	if err != nil {
		return errors.New("could not create the canary team")
	}

	apiKey, err := keys.GenerateKey(keys.ApiKeyPrefix)
	if err != nil {
		return errors.New("could not generate the canary API key")
	}

	keyRow, err := queries.CreateTeamAPIKey(ctx, authqueries.CreateTeamAPIKeyParams{
		TeamID:           state.TeamID,
		CreatedBy:        nil,
		ApiKeyHash:       apiKey.HashedValue,
		ApiKeyPrefix:     apiKey.Masked.Prefix,
		ApiKeyLength:     int32(apiKey.Masked.ValueLength),
		ApiKeyMaskPrefix: apiKey.Masked.MaskedValuePrefix,
		ApiKeyMaskSuffix: apiKey.Masked.MaskedValueSuffix,
		Name:             "Monad SDK canary " + suffix,
	})
	if err != nil {
		return errors.New("could not create the canary API-key row")
	}
	state.APIKeyID = &keyRow.ID
	if err := replaceStateFile(cfg.statePath, state); err != nil {
		return err
	}

	secretVersion, err := addSecretVersion(ctx, cfg, secretID, apiKey.PrefixedRawValue)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return errors.New("could not commit the canary database transaction")
	}
	committed = true
	keepState = true

	fmt.Printf("Canary credential created without printing the raw API key.\n")
	fmt.Printf("Team ID: %s\n", state.TeamID)
	fmt.Printf("Team slug: %s\n", teamSlug)
	fmt.Printf("API key ID: %s\n", keyRow.ID)
	fmt.Printf("Secret ID: %s\n", secretID)
	fmt.Printf("Secret version: %s\n", secretVersion)
	fmt.Printf("Reconciliation state: %s\n", cfg.statePath)
	fmt.Printf("After the canary succeeds, rerun this command with --cleanup and the same --state-file.\n")

	return nil
}

func cleanup(ctx context.Context, cfg config) error {
	state, err := readStateFile(cfg.statePath)
	if err != nil {
		return err
	}
	if err := reconcile(ctx, cfg, state); err != nil {
		return err
	}
	if err := removeStateFile(cfg.statePath); err != nil {
		return errors.New("could not remove the reconciled canary state file")
	}

	fmt.Printf("Canary API key, team, and Secret Manager secret deleted.\n")
	fmt.Printf("Reconciliation state removed: %s\n", cfg.statePath)
	return nil
}

func reconcile(ctx context.Context, cfg config, state bootstrapState) error {
	secretCfg := cfg
	secretCfg.project = state.Project
	secretExists, err := inspectCanarySecret(ctx, secretCfg, state)
	if err != nil {
		return err
	}

	db, err := authdb.NewClient(ctx, cfg.databaseURL)
	if err != nil {
		return errors.New("could not connect to the canary database for cleanup")
	}
	defer db.Close()

	queries, tx, err := db.WithTx(ctx)
	if err != nil {
		return errors.New("could not start the canary cleanup transaction")
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	if err := verifyCanaryDatabaseIdentity(ctx, tx, state); err != nil {
		return err
	}
	if state.APIKeyID != nil {
		_, err := queries.DeleteTeamAPIKey(ctx, authqueries.DeleteTeamAPIKeyParams{
			ID:     *state.APIKeyID,
			TeamID: state.TeamID,
		})
		if err != nil {
			return errors.New("could not delete the canary API-key row")
		}
	}
	if err := queries.DeleteTeamByID(ctx, state.TeamID); err != nil {
		return errors.New("could not delete the canary team")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("could not commit the canary database cleanup")
	}
	committed = true

	if secretExists {
		if err := deleteSecret(ctx, secretCfg, state.SecretID); err != nil {
			return errors.New("could not delete the canary Secret Manager secret")
		}
	}

	return nil
}

func verifyCanaryDatabaseIdentity(ctx context.Context, tx pgx.Tx, state bootstrapState) error {
	var email string
	var name string
	var slug string
	err := tx.QueryRow(
		ctx,
		`SELECT email, name, slug FROM public.teams WHERE id = $1::uuid FOR UPDATE`,
		state.TeamID,
	).Scan(&email, &name, &slug)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("could not inspect the canary team before cleanup")
	}
	if err == nil && !canaryTeamIdentityMatches(state.Suffix, email, name, slug) {
		return errors.New("refusing cleanup: persisted team ID is not the generated canary team")
	}

	if state.APIKeyID == nil {
		return nil
	}

	var keyTeamID uuid.UUID
	var keyName string
	err = tx.QueryRow(
		ctx,
		`SELECT team_id, name FROM public.team_api_keys WHERE id = $1::uuid FOR UPDATE`,
		*state.APIKeyID,
	).Scan(&keyTeamID, &keyName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("could not inspect the canary API key before cleanup")
	}
	if err == nil &&
		(keyTeamID != state.TeamID || keyName != "Monad SDK canary "+state.Suffix) {
		return errors.New("refusing cleanup: persisted API-key ID is not owned by the generated canary team")
	}

	return nil
}

func canaryTeamIdentityMatches(suffix string, email string, name string, slug string) bool {
	return email == "monad-sdk-canary+"+suffix+"@example.invalid" &&
		name == "Monad SDK canary "+suffix &&
		slug == "monad-sdk-canary-"+suffix
}

func createStateFile(path string, state bootstrapState) error {
	content, err := encodeState(state)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("state file already exists; reconcile it with --cleanup before creating another canary")
		}
		return errors.New("could not create the canary reconciliation state")
	}

	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return errors.New("could not write the canary reconciliation state")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("could not sync the canary reconciliation state")
	}
	if err := file.Close(); err != nil {
		return errors.New("could not close the canary reconciliation state")
	}
	if err := syncParentDirectory(path); err != nil {
		return errors.New("could not sync the canary reconciliation state directory")
	}

	return nil
}

func replaceStateFile(path string, state bootstrapState) error {
	content, err := encodeState(state)
	if err != nil {
		return err
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".canary-reconcile-*")
	if err != nil {
		return errors.New("could not create a temporary reconciliation state")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return errors.New("could not protect the temporary reconciliation state")
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return errors.New("could not write the temporary reconciliation state")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return errors.New("could not sync the temporary reconciliation state")
	}
	if err := temp.Close(); err != nil {
		return errors.New("could not close the temporary reconciliation state")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.New("could not replace the canary reconciliation state")
	}
	if err := syncParentDirectory(path); err != nil {
		return errors.New("could not sync the canary reconciliation state directory")
	}

	return nil
}

func removeStateFile(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncParentDirectory(path)
}

func syncParentDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()

	return directory.Sync()
}

func readStateFile(path string) (bootstrapState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return bootstrapState{}, errors.New("could not inspect the canary reconciliation state")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return bootstrapState{}, errors.New("canary reconciliation state must be a private regular file")
	}

	file, err := os.Open(path)
	if err != nil {
		return bootstrapState{}, errors.New("could not open the canary reconciliation state")
	}
	defer file.Close()

	var state bootstrapState
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return bootstrapState{}, errors.New("could not decode the canary reconciliation state")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return bootstrapState{}, errors.New("canary reconciliation state contains trailing data")
	}

	switch {
	case state.Version != stateVersion:
		return bootstrapState{}, errors.New("canary reconciliation state has an unsupported version")
	case state.Project == "":
		return bootstrapState{}, errors.New("canary reconciliation state has no project")
	case !secretIDPattern.MatchString(state.SecretID):
		return bootstrapState{}, errors.New("canary reconciliation state has an invalid secret ID")
	case !canarySuffixPattern.MatchString(state.Suffix):
		return bootstrapState{}, errors.New("canary reconciliation state has an invalid suffix")
	case !strings.HasSuffix(state.SecretID, "-"+state.Suffix):
		return bootstrapState{}, errors.New("canary reconciliation state secret does not match its suffix")
	case state.TeamID == uuid.Nil:
		return bootstrapState{}, errors.New("canary reconciliation state has no team ID")
	case state.APIKeyID != nil && *state.APIKeyID == uuid.Nil:
		return bootstrapState{}, errors.New("canary reconciliation state has an invalid API-key ID")
	}

	return state, nil
}

func encodeState(state bootstrapState) ([]byte, error) {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, errors.New("could not encode the canary reconciliation state")
	}
	return append(content, '\n'), nil
}

func createSecret(ctx context.Context, cfg config, secretID string) error {
	cmd := gcloudCommand(
		ctx,
		cfg,
		"secrets", "create", secretID,
		"--project", cfg.project,
		"--replication-policy", "automatic",
		"--labels", "purpose=monad-sdk-canary",
		"--quiet",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return errors.New("could not create the unique Secret Manager secret")
	}

	return nil
}

func addSecretVersion(ctx context.Context, cfg config, secretID string, rawAPIKey string) (string, error) {
	var stdout bytes.Buffer
	cmd := gcloudCommand(
		ctx,
		cfg,
		"secrets", "versions", "add", secretID,
		"--project", cfg.project,
		"--data-file=-",
		"--format=value(name)",
		"--quiet",
	)
	cmd.Stdin = strings.NewReader(rawAPIKey)
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return "", errors.New("could not write the canary API key to Secret Manager")
	}

	version := strings.TrimSpace(stdout.String())
	if version == "" {
		return "", errors.New("Secret Manager did not return the created version name")
	}

	return version, nil
}

func deleteSecret(ctx context.Context, cfg config, secretID string) error {
	cmd := gcloudCommand(
		ctx,
		cfg,
		"secrets", "delete", secretID,
		"--project", cfg.project,
		"--quiet",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	return cmd.Run()
}

type secretDescription struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

func inspectCanarySecret(ctx context.Context, cfg config, state bootstrapState) (bool, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := gcloudCommand(
		ctx,
		cfg,
		"secrets", "describe", state.SecretID,
		"--project", cfg.project,
		"--format=json(name,labels)",
		"--quiet",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		message := strings.ToUpper(stderr.String())
		if strings.Contains(message, "NOT_FOUND") || strings.Contains(message, "NOT FOUND") {
			return false, nil
		}
		return false, errors.New("could not inspect the canary Secret Manager secret")
	}

	var description secretDescription
	decoder := json.NewDecoder(io.LimitReader(&stdout, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&description); err != nil {
		return false, errors.New("could not decode the canary Secret Manager identity")
	}
	if description.Labels["purpose"] != "monad-sdk-canary" ||
		!strings.HasSuffix(description.Name, "/secrets/"+state.SecretID) {
		return false, errors.New("refusing cleanup: persisted secret is not labelled as the generated canary secret")
	}

	return true, nil
}

func gcloudCommand(ctx context.Context, cfg config, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, cfg.gcloud, args...)
	cmd.Env = filteredEnvironment(os.Environ())

	return cmd
}

func filteredEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		switch name {
		case "POSTGRES_CONNECTION_STRING", "E2B_API_KEY", "E2B_ACCESS_TOKEN":
			continue
		default:
			filtered = append(filtered, item)
		}
	}

	return filtered
}
