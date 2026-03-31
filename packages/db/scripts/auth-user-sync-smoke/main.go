package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

const (
	defaultCount = 1
	defaultWait  = 30 * time.Second
)

type config struct {
	ConnectionString string
	Count            int
	Wait             time.Duration
}

type authUser struct {
	ID    uuid.UUID
	Email string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()

	if err != nil {
		fmt.Fprintf(os.Stderr, "auth user sync smoke failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	db, err := authdb.NewClient(ctx, cfg.ConnectionString, "")
	if err != nil {
		return fmt.Errorf("create auth db client: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close auth db client: %v\n", closeErr)
		}
	}()

	users := newAuthUsers(cfg.Count)
	insertedUsers := make([]authUser, 0, len(users))

	defer func() {
		if len(insertedUsers) == 0 {
			return
		}

		if cleanupErr := cleanupInsertedUsers(ctx, db, insertedUsers); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "cleanup auth users: %v\n", cleanupErr)

			return
		}

		fmt.Fprintf(os.Stdout, "cleaned up %d auth.users rows\n", len(insertedUsers))
	}()

	if err := insertUsers(ctx, db, users, &insertedUsers); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "created %d auth.users rows\n", len(insertedUsers))
	for _, user := range insertedUsers {
		fmt.Fprintf(os.Stdout, "  %s %s\n", user.ID.String(), user.Email)
	}

	fmt.Fprintf(os.Stdout, "waiting %s before delete\n", cfg.Wait)

	timer := time.NewTimer(cfg.Wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}

	if err := cleanupInsertedUsers(ctx, db, insertedUsers); err != nil {
		return fmt.Errorf("delete auth users: %w", err)
	}

	insertedUsers = nil
	fmt.Fprintln(os.Stdout, "deleted auth.users rows")

	return nil
}

func loadConfig() (config, error) {
	connectionString := strings.TrimSpace(os.Getenv("AUTH_DB_CONNECTION_STRING"))
	if connectionString == "" {
		connectionString = strings.TrimSpace(os.Getenv("POSTGRES_CONNECTION_STRING"))
	}
	if connectionString == "" {
		return config{}, fmt.Errorf("AUTH_DB_CONNECTION_STRING or POSTGRES_CONNECTION_STRING must be set")
	}

	count, err := loadCount()
	if err != nil {
		return config{}, err
	}

	wait, err := loadWait()
	if err != nil {
		return config{}, err
	}

	return config{
		ConnectionString: connectionString,
		Count:            count,
		Wait:             wait,
	}, nil
}

func loadCount() (int, error) {
	rawCount := strings.TrimSpace(os.Getenv("AUTH_USER_SYNC_SMOKE_COUNT"))
	if rawCount == "" {
		return defaultCount, nil
	}

	count, err := strconv.Atoi(rawCount)
	if err != nil {
		return 0, fmt.Errorf("parse AUTH_USER_SYNC_SMOKE_COUNT: %w", err)
	}
	if count < 1 {
		return 0, fmt.Errorf("AUTH_USER_SYNC_SMOKE_COUNT must be at least 1")
	}

	return count, nil
}

func loadWait() (time.Duration, error) {
	rawWait := strings.TrimSpace(os.Getenv("AUTH_USER_SYNC_SMOKE_WAIT"))
	if rawWait == "" {
		return defaultWait, nil
	}

	wait, err := time.ParseDuration(rawWait)
	if err != nil {
		return 0, fmt.Errorf("parse AUTH_USER_SYNC_SMOKE_WAIT: %w", err)
	}
	if wait <= 0 {
		return 0, fmt.Errorf("AUTH_USER_SYNC_SMOKE_WAIT must be greater than 0")
	}

	return wait, nil
}

func newAuthUsers(count int) []authUser {
	runID := strings.ReplaceAll(uuid.NewString(), "-", "")
	users := make([]authUser, 0, count)

	for i := range count {
		userID := uuid.New()
		email := fmt.Sprintf("auth-sync-smoke-%s-%02d@example.com", runID[:12], i+1)
		users = append(users, authUser{
			ID:    userID,
			Email: email,
		})
	}

	return users
}

func cleanupInsertedUsers(ctx context.Context, db *authdb.Client, users []authUser) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	return deleteUsers(cleanupCtx, db, users)
}

func insertUsers(ctx context.Context, db *authdb.Client, users []authUser, insertedUsers *[]authUser) error {
	for _, user := range users {
		err := db.TestsRawSQL(ctx, `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, user.ID, user.Email)
		if err != nil {
			return fmt.Errorf("insert auth user %s: %w", user.Email, err)
		}

		*insertedUsers = append(*insertedUsers, user)
	}

	return nil
}

func deleteUsers(ctx context.Context, db *authdb.Client, users []authUser) error {
	for _, user := range users {
		err := db.TestsRawSQL(ctx, `
DELETE FROM auth.users
WHERE id = $1
`, user.ID)
		if err != nil {
			return fmt.Errorf("delete auth user %s: %w", user.Email, err)
		}
	}

	return nil
}
