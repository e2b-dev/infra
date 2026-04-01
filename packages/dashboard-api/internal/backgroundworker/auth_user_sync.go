package backgroundworker

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const AuthUserSyncKind = "auth_user_sync"

type AuthUserSyncArgs struct {
	UserID    string `json:"user_id"`
	Operation string `json:"operation"`
	Email     string `json:"email,omitempty"`
}

func (AuthUserSyncArgs) Kind() string { return AuthUserSyncKind }

type AuthUserSyncWorker struct {
	river.WorkerDefaults[AuthUserSyncArgs]

	mainDB      *sqlcdb.Client
	l           logger.Logger
	jobsCounter metric.Int64Counter
}

func NewAuthUserSyncWorker(mainDB *sqlcdb.Client, l logger.Logger) *AuthUserSyncWorker {
	jobsCounter, err := otel.Meter("github.com/e2b-dev/infra/packages/dashboard-api/internal/backgroundworker")
		"dashboard_api.auth_user_sync.jobs_total",
		metric.WithDescription("Total auth user sync jobs by operation and result."),
		metric.WithUnit("{job}"),
	)
	if err != nil {
		l.Warn(context.Background(), "failed to initialize auth user sync metric", zap.Error(err))
	}

	return &AuthUserSyncWorker{
		mainDB:      mainDB,
		l:           l,
		jobsCounter: jobsCounter,
	}
}

func (w *AuthUserSyncWorker) Work(ctx context.Context, job *river.Job[AuthUserSyncArgs]) error {
	attrs := []attribute.KeyValue{
		attribute.String("job.kind", AuthUserSyncKind),
		attribute.String("job.operation", job.Args.Operation),
		attribute.Int64("job.id", job.ID),
		telemetry.WithUserID(job.Args.UserID),
	}
	telemetry.ReportEvent(ctx, "auth_user_sync.job.started", attrs...)

	userID, err := uuid.Parse(job.Args.UserID)
	if err != nil {
		telemetry.ReportError(ctx, "auth user sync parse user_id", err, attrs...)
		w.observeJob(ctx, job.Args.Operation, "error")

		return fmt.Errorf("parse user_id %q: %w", job.Args.UserID, err)
	}

	w.l.Info(ctx, "processing auth user sync job",
		zap.String("job.kind", AuthUserSyncKind),
		zap.Int64("job.id", job.ID),
		zap.String("job.operation", job.Args.Operation),
		logger.WithUserID(job.Args.UserID),
		zap.Int("job.attempt", job.Attempt),
	)

	switch job.Args.Operation {
	case "delete":
		if err := w.mainDB.DeletePublicUser(ctx, userID); err != nil {
			telemetry.ReportError(ctx, "auth user sync delete public user", err, attrs...)
			w.observeJob(ctx, job.Args.Operation, "error")

			return fmt.Errorf("delete public.users %s: %w", userID, err)
		}

	case "upsert":
		if job.Args.Email == "" {
			err := fmt.Errorf("missing email in job args")
			telemetry.ReportError(ctx, "auth user sync missing email", err, attrs...)
			w.observeJob(ctx, job.Args.Operation, "error")

			return fmt.Errorf("upsert public.users %s: missing email in job args", userID)
		}

		if err := w.mainDB.UpsertPublicUser(ctx, queries.UpsertPublicUserParams{
			ID:    userID,
			Email: job.Args.Email,
		}); err != nil {
			telemetry.ReportError(ctx, "auth user sync upsert public user", err, attrs...)
			w.observeJob(ctx, job.Args.Operation, "error")

			return fmt.Errorf("upsert public.users %s: %w", userID, err)
		}

	default:
		err := fmt.Errorf("unknown operation %q", job.Args.Operation)
		telemetry.ReportError(ctx, "auth user sync unknown operation", err, attrs...)
		w.observeJob(ctx, job.Args.Operation, "error")

		return fmt.Errorf("unknown operation %q for user %s", job.Args.Operation, userID)
	}

	w.l.Info(ctx, "completed auth user sync job",
		zap.String("job.kind", AuthUserSyncKind),
		zap.Int64("job.id", job.ID),
		zap.String("job.operation", job.Args.Operation),
		logger.WithUserID(job.Args.UserID),
	)
	telemetry.ReportEvent(ctx, "auth_user_sync.job.completed", attrs...)
	w.observeJob(ctx, job.Args.Operation, "success")

	return nil
}

func (w *AuthUserSyncWorker) observeJob(ctx context.Context, operation, result string) {
	if w.jobsCounter == nil {
		return
	}

	w.jobsCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("worker", "supabase_auth_user_sync"),
		attribute.String("job.kind", AuthUserSyncKind),
		attribute.String("job.operation", operation),
		attribute.String("result", result),
	))
}
