// Package storageindex pushes build metadata to the storage index service so it
// can track template/build storage for lifecycle management. Sending the metadata
// is safe to retry and can be backfilled.
package storageindex

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	storageapi "github.com/e2b-dev/infra/packages/shared/pkg/grpc/storage-api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const ingestTimeout = 30 * time.Second

// Build is the attribution for a build being ingested.
type Build struct {
	BuildID   string
	TeamID    string
	BuildType string // storage.ObjectOrigin value: pause | template_build | ...
	CreatedAt int64  // build creation time, unix nanos
}

// Artifact pairs a stored artifact name (the path component the reaper deletes,
// e.g. storage.MemfileName) with its diff-header future. The header is resolved
// inside Ingest, so callers can pass the still-pending header.
type Artifact struct {
	Name   string
	Header *utils.SetOnce[*header.Header]
}

// Ingest records a build in the storage index after its headers are ready (it does
// not wait for the upload). It is a no-op when the client is unconfigured or the
// feature flag is off, so call sites stay unconditional.
//
// By default a failure is a soft error: it is logged asynchronously and Ingest
// returns nil so the build proceeds. With the hard-error flag set, Ingest runs
// synchronously and returns the failure so the caller can fail the build.
func Ingest(ctx context.Context, client *storageapi.Client, ff *featureflags.Client, log logger.Logger, b Build, arts ...Artifact) error {
	if client == nil || !ff.BoolFlag(ctx, featureflags.StorageAPIIngestFlag) {
		return nil
	}

	if ff.BoolFlag(ctx, featureflags.StorageAPIIngestHardErrorFlag) {
		ctx, cancel := context.WithTimeout(ctx, ingestTimeout)
		defer cancel()

		return ingest(ctx, client, b, arts...)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ingestTimeout)
		defer cancel()

		if err := ingest(ctx, client, b, arts...); err != nil {
			log.Warn(ctx, "storage index ingest failed", zap.String("build_id", b.BuildID), zap.Error(err))
		}
	}()

	return nil
}

func ingest(ctx context.Context, client *storageapi.Client, b Build, arts ...Artifact) error {
	req := &storageapi.IngestBuildRequest{
		BuildId:   b.BuildID,
		TeamId:    b.TeamID,
		BuildType: b.BuildType,
		CreatedAt: b.CreatedAt,
	}
	for _, a := range arts {
		if a.Header == nil {
			continue
		}
		h, err := a.Header.WaitWithContext(ctx)
		if err != nil {
			return fmt.Errorf("wait for %s header: %w", a.Name, err)
		}
		if h == nil {
			continue
		}
		req.Artifacts = append(req.Artifacts, toArtifact(a.Name, h))
	}
	if len(req.Artifacts) == 0 {
		return nil
	}

	if err := client.IngestBuild(ctx, req); err != nil {
		return fmt.Errorf("ingest build %s: %w", b.BuildID, err)
	}

	return nil
}

func toArtifact(name string, h *header.Header) *storageapi.Artifact {
	self := h.Metadata.BuildId
	ft := h.GetBuildFrameData(self)

	a := &storageapi.Artifact{
		Name:       name,
		Size:       int64(h.Metadata.Size),
		Compressed: ft != nil && ft != storage.UncompressedFrameTable,
	}
	for _, id := range h.Mapping.Builds() {
		if id == self || id == uuid.Nil {
			continue
		}
		a.Refs = append(a.Refs, &storageapi.Ref{BuildId: id.String()})
	}

	return a
}
