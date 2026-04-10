package backgroundworker

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	workerMeter  = otel.Meter(workerMeterName)
	workerTracer = otel.Tracer(workerMeterName)
)

type AuthUserSyncArgs struct {
	UserID    string `json:"user_id"`
	Operation string `json:"operation"`
}

func (AuthUserSyncArgs) Kind() string { return authUserProjectionKind }

var _ river.Worker[AuthUserSyncArgs] = (*AuthUserSyncWorker)(nil)

type AuthUserSyncWorker struct {
	river.WorkerDefaults[AuthUserSyncArgs]

	supabaseDB  *supabasedb.Client
	authDB      *sqlcdb.Client
	l           logger.Logger
	jobsCounter metric.Int64Counter
}

func NewAuthUserSyncWorker(ctx context.Context, supabaseDB *supabasedb.Client, authDB *sqlcdb.Client, l logger.Logger) *AuthUserSyncWorker {
	jobsCounter, err := workerMeter.Int64Counter(
		"jobs_total",
		metric.WithDescription("Total auth user sync jobs by operation and result."),
		metric.WithUnit("{job}"),
	)
	if err != nil {
		l.Warn(ctx, "failed to initialize auth user sync metric", zap.Error(err))
	}

	return &AuthUserSyncWorker{
		supabaseDB:  supabaseDB,
		authDB:      authDB,
		l:           l,
		jobsCounter: jobsCounter,
	}
}

func (w *AuthUserSyncWorker) Work(ctx context.Context, job *river.Job[AuthUserSyncArgs]) error {
	ctx, span := workerTracer.Start(ctx, "auth_user_sync.work", trace.WithAttributes(
		attribute.String("job.kind", authUserProjectionKind),
		attribute.String("job.operation", job.Args.Operation),
		attribute.Int64("job.id", job.ID),
		telemetry.WithUserID(job.Args.UserID),
	))
	defer span.End()

	telemetry.ReportEvent(ctx, "auth_user_sync.job.started")

	userID, err := uuid.Parse(job.Args.UserID)
	if err != nil {
		telemetry.ReportError(ctx, "auth user sync parse user_id", err)
		w.observeJob(ctx, job.Args.Operation, jobResultInvalidArgument)

		return river.JobCancel(fmt.Errorf("parse user_id %q: %w", job.Args.UserID, err))
	}

	w.l.Info(ctx, "processing auth user sync job",
		zap.String("job.kind", authUserProjectionKind),
		zap.Int64("job.id", job.ID),
		zap.String("job.operation", job.Args.Operation),
		logger.WithUserID(job.Args.UserID),
		zap.Int("job.attempt", job.Attempt),
	)

	switch job.Args.Operation {
	case "delete":
		if err := w.authDB.DeletePublicUser(ctx, userID); err != nil {
			telemetry.ReportError(ctx, "auth user sync delete public user", err)
			w.observeJob(ctx, job.Args.Operation, jobResultError)

			return fmt.Errorf("delete public.users %s: %w", userID, err)
		}

	case "upsert":
		supabaseUser, err := w.supabaseDB.Write.GetAuthUserByID(ctx, userID)
		if dberrors.IsNotFoundError(err) {
			if err := w.authDB.DeletePublicUser(ctx, userID); err != nil {
				telemetry.ReportError(ctx, "auth user sync delete stale public user", err)
				w.observeJob(ctx, job.Args.Operation, jobResultError)

				return fmt.Errorf("delete stale public.users %s: %w", userID, err)
			}

			telemetry.ReportEvent(ctx, "auth_user_sync.job.source_user_missing")
			w.observeJob(ctx, job.Args.Operation, jobResultSuccess)

			return nil
		}
		if err != nil {
			telemetry.ReportError(ctx, "auth user sync get source user", err)
			w.observeJob(ctx, job.Args.Operation, jobResultError)

			return fmt.Errorf("get source auth.users %s: %w", userID, err)
		}

		if supabaseUser.Email == "" {
			err := fmt.Errorf("missing email in source user %s", userID)
			telemetry.ReportError(ctx, "auth user sync missing source email", err)
			w.observeJob(ctx, job.Args.Operation, jobResultInvalidArgument)

			return river.JobCancel(err)
		}

		if err := w.authDB.UpsertPublicUser(ctx, queries.UpsertPublicUserParams{
			ID:    userID,
			Email: supabaseUser.Email,
		}); err != nil {
			telemetry.ReportError(ctx, "auth user sync upsert public user", err)
			w.observeJob(ctx, job.Args.Operation, jobResultError)

			return fmt.Errorf("upsert public.users %s: %w", userID, err)
		}

	default:
		err := fmt.Errorf("unknown operation %q for user %s", job.Args.Operation, userID)
		telemetry.ReportError(ctx, "auth user sync unknown operation", err)
		w.observeJob(ctx, job.Args.Operation, jobResultInvalidArgument)

		return river.JobCancel(err)
	}

	w.l.Info(ctx, "completed auth user sync job",
		zap.String("job.kind", authUserProjectionKind),
		zap.Int64("job.id", job.ID),
		zap.String("job.operation", job.Args.Operation),
		logger.WithUserID(job.Args.UserID),
	)
	telemetry.ReportEvent(ctx, "auth_user_sync.job.completed")
	w.observeJob(ctx, job.Args.Operation, jobResultSuccess)

	return nil
}

func (w *AuthUserSyncWorker) observeJob(ctx context.Context, operation, result string) {
	if w.jobsCounter == nil {
		return
	}

	w.jobsCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("worker", authUserProjectionKind),
		attribute.String("job.kind", authUserProjectionKind),
		attribute.String("job.operation", operation),
		attribute.String("result", result),
	))
}
