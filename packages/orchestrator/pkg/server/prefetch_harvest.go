package server

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// minHarvestTimeoutMs is the floor applied to the harvest-timeout flag, so a
// misconfigured zero/negative value can't yield an already-expired context that
// silently skips every harvest.
const minHarvestTimeoutMs = 1000

// harvestReapTimeout bounds the throwaway teardown so a stuck Stop/Close can't
// hold the start slot (released only after the reap) indefinitely.
const harvestReapTimeout = 60 * time.Second

// harvestResumePrefetchAsync records a resume page-fault trace for a freshly
// paused sandbox and (optionally) persists it as a prefetch mapping, so the
// customer's next resume of this snapshot can replay it.
//
// It is the pause-side analogue of the resume+harvest that Checkpoint already
// does, with these differences: the resumed instance is a throwaway (network
// isolated, kept out of the live registry, never promoted to a live sandbox),
// and the harvested mapping is carried through the same-version pause metadata
// (which otherwise drops Prefetch).
//
// It is best-effort and runs AFTER the Pause RPC has returned, alongside the
// in-flight snapshot upload: it never affects the pause result, the local
// snapshot is already in the cache, and the consume path waits for the upload
// before touching metadata. Both gates default off, so this is a no-op until
// explicitly enabled.
func (s *Server) harvestResumePrefetchAsync(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	res *snapshotResult,
	buildID string,
	objectMetadata storage.ObjectMetadata,
) {
	// Flag checks run synchronously (ctx still carries the per-sandbox LD
	// context the Pause handler attached) before we detach into a goroutine.
	if !s.featureFlags.BoolFlag(ctx, featureflags.PauseResumePrefetchHarvestFlag) {
		return
	}

	timeoutMs := s.featureFlags.IntFlag(ctx, featureflags.PauseResumePrefetchHarvestTimeoutMsFlag)
	// Guard against a misconfigured (zero/negative) flag value, which would yield
	// an already-expired context and silently skip every harvest.
	timeoutMs = max(timeoutMs, minHarvestTimeoutMs)
	// When off, the harvest still runs and logs its trace size but persists
	// nothing — so resumes are unaffected and we can validate harvest behaviour
	// with no customer-visible change before enabling prefetch on resume.
	consume := s.featureFlags.BoolFlag(ctx, featureflags.PauseResumePrefetchConsumeFlag)

	go func() {
		// Detach from the request (Pause has returned) but keep the LD context
		// values; bound the whole harvest so a stuck resume can't pin the slot.
		hCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()

		hCtx, span := tracer.Start(hCtx, "harvest-resume-prefetch", trace.WithNewRoot())
		defer span.End()
		span.SetAttributes(
			attribute.String("build_id", buildID),
			attribute.Bool("consume", consume),
		)

		start := time.Now()
		pages, err := s.runResumePrefetchHarvest(hCtx, sbx, res, buildID, objectMetadata, consume)
		span.SetAttributes(
			attribute.Int64("harvest.duration_ms", time.Since(start).Milliseconds()),
			attribute.Int("harvest.pages", pages),
		)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			logger.L().Warn(hCtx, "pause-resume prefetch harvest failed",
				logger.WithSandboxID(sbx.Runtime.SandboxID),
				logger.WithBuildID(buildID),
				zap.Error(err),
			)
		}
	}()
}

// runResumePrefetchHarvest performs the throwaway warm resume, collects the
// fault trace, and (when consume is set) persists the mapping into the pause
// artifact metadata locally and remotely. Returns the harvested page count.
func (s *Server) runResumePrefetchHarvest(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	res *snapshotResult,
	buildID string,
	objectMetadata storage.ObjectMetadata,
	consume bool,
) (int, error) {
	// The throwaway resume and its start slot are confined to this call, so they
	// are released before we wait on the upload below.
	mapping, err := s.harvestResumeMapping(ctx, sbx, buildID)
	if err != nil {
		return 0, err
	}

	pages := mapping.Count() // nil-safe

	if !consume || mapping == nil {
		// Harvest-only: trace measured (page count returned/logged), persist
		// nothing, so the customer's resume is unaffected.
		return pages, nil
	}

	// The async snapshot upload (uploadSnapshotAsync) is still in flight: it reads
	// this build's local metafile and writes the remote metadata object, with
	// retries, from another goroutine. Rewriting the metafile in place (the local
	// update) or the remote object while it runs would risk a torn read or a
	// clobbered mapping, so wait for it to finish first. Once Wait returns on its
	// own the upload is done with both files; if instead the harvest deadline
	// fired we must leave them untouched.
	uploadErr := res.upload.Wait(ctx)
	if ctx.Err() != nil {
		return pages, nil //nolint:nilerr // harvest deadline fired while the upload was still running; leave its metadata untouched
	}

	// Carry the mapping through the same-version pause metadata. The local cache
	// update is enough for a same-node resume, so do it regardless of whether the
	// remote upload succeeded.
	meta := res.meta.WithPrefetch(&metadata.Prefetch{Memory: mapping})
	if err := s.templateCache.UpdateMetadata(buildID, meta); err != nil {
		return pages, fmt.Errorf("update local metadata: %w", err)
	}

	// Only enrich the remote metadata if the snapshot actually landed; on upload
	// failure the remote build is incomplete, so there is nothing to enrich (the
	// local update above still lets a same-node resume prefetch).
	if uploadErr != nil {
		return pages, nil //nolint:nilerr // remote snapshot did not land; the local update is the most we can do
	}
	if err := metadata.UploadMetadata(ctx, s.persistence, meta, objectMetadata); err != nil {
		return pages, fmt.Errorf("re-upload metadata: %w", err)
	}

	return pages, nil
}

// harvestResumeMapping resumes a throwaway warm copy of the just-paused
// snapshot, records its resume page-fault trace, and returns it as a prefetch
// mapping (nil if the trace was empty). The throwaway and the start slot it
// holds are both released before this returns, so the caller can persist the
// mapping without pinning node resources.
func (s *Server) harvestResumeMapping(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	buildID string,
) (*metadata.MemoryPrefetchMapping, error) {
	// Bound concurrent harvests the same way real starts are bounded, so a burst
	// of pauses can't overcommit the node. If we can't acquire within the
	// harvest deadline the run is simply skipped (best-effort).
	if err := s.waitForAcquire(ctx); err != nil {
		return nil, fmt.Errorf("acquire start slot: %w", err)
	}
	defer s.startingSandboxes.Release(1)

	// Load the just-written snapshot from the LOCAL cache (warm): the harvest
	// pays no cold GCS/NFS fetch, only a local re-fault. isSnapshot=true mirrors
	// Checkpoint's resume. The pause artifact carries no Prefetch (SameVersion
	// dropped it), so no prefetcher runs and the trace is clean demand faults.
	tmpl, err := s.templateCache.GetTemplate(ctx, buildID, true, false,
		sbxtemplate.GetTemplateOpts{MaxSandboxLengthHours: sbx.Config.MaxSandboxLengthHours})
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}

	// Throwaway identity: distinct SandboxID/ExecutionID from the (being-stopped)
	// original so it never collides in the sandbox map. ResumeSandbox registers
	// it in the factory's sandbox table (for network assignment and health), but
	// it is never added to the server lifecycle or proxy pool and is reaped here,
	// so it is not externally addressable.
	runtime := sandbox.RuntimeMetadata{
		TemplateID:  sbx.Runtime.TemplateID,
		SandboxID:   "prefetch-harvest-" + sbx.Runtime.SandboxID,
		ExecutionID: uuid.NewString(),
		TeamID:      sbx.Runtime.TeamID,
		// The throwaway resumes the just-written pause snapshot (buildID), not the
		// original sandbox's build, so tag it with buildID for correct attribution.
		BuildID:     buildID,
		SandboxType: sbx.Runtime.SandboxType,
	}

	// Suppress volume mounts on the throwaway. The throwaway is network-isolated
	// (DenyEgress drops all guest egress, including to the orchestrator IP that
	// fronts the NFS proxy), so the synchronous foreground NFS mount envd runs at
	// /init for a volume-mounted sandbox would block and fail the resume. The
	// prefetch mapping is memfile-only: page-cache pages resident at pause fault
	// back from the memfile, not over NFS, and NFS data not already cached was
	// never a memfile page — so dropping the mount loses no prefetchable coverage
	// while letting volume-mounted sandboxes harvest cleanly. Clone the config so
	// the live original's is untouched.
	harvestConfig := *sbx.Config
	harvestConfig.VolumeMounts = nil

	// Isolate the throwaway from the network: the user workload stays frozen
	// until envd /init completes and the instance is reaped right after, but
	// envd /init itself (and any briefly unfrozen workload) must not reach the
	// network. WithDenyEgress drops ALL guest egress before the resume (the
	// orchestrator drives the resume by connecting into the guest), so the
	// harvested resume stays inert externally.
	resumedSbx, err := s.sandboxFactory.ResumeSandbox(
		ctx,
		tmpl,
		&harvestConfig,
		runtime,
		sbx.GetStartedAt(),
		sbx.GetEndAt(),
		// Throwaway: no API config to store (it is never restarted/addressable).
		nil,
		sandbox.WithDenyEgress(),
		sandbox.WithoutLiveRegistration(),
	)
	if err != nil {
		return nil, fmt.Errorf("resume throwaway: %w", err)
	}

	// Reap the throwaway on every path — it is never promoted to a live sandbox.
	// Detach from the harvest deadline so teardown still runs after a timeout, but
	// bound it so a stuck Stop/Close can't pin the start slot (held until this
	// returns) indefinitely.
	defer func() {
		reapCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), harvestReapTimeout)
		defer cancel()
		if stopErr := resumedSbx.Stop(reapCtx); stopErr != nil {
			sbxlogger.I(resumedSbx).Warn(reapCtx, "harvest: failed to stop throwaway", zap.Error(stopErr))
		}
		if closeErr := resumedSbx.Close(reapCtx); closeErr != nil {
			sbxlogger.I(resumedSbx).Warn(reapCtx, "harvest: failed to close throwaway", zap.Error(closeErr))
		}
	}()

	// ResumeSandbox blocks on WaitForEnvd -> initEnvd, so by here the trace
	// covers the full resume-through-envd-init working set.
	prefetchData, err := resumedSbx.MemoryPrefetchData(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect prefetch data: %w", err)
	}

	return metadata.PrefetchEntriesToMapping(
		slices.Collect(maps.Values(prefetchData.BlockEntries)),
		prefetchData.BlockSize,
	), nil
}
