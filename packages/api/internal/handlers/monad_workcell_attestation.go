package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	monadMetadataProvider         = "monad.workcell.provider"
	monadMetadataTemplateID       = "monad.workcell.template-id"
	monadMetadataImageID          = "monad.workcell.image-id"
	monadMetadataIdentityFidelity = "monad.workcell.identity-fidelity"
	monadMetadataPlacement        = "monad.workcell.placement"

	monadProviderE2B              = "e2b"
	monadIdentityImageAttested    = "image-attested"
	monadAttestationConflictError = "Sandbox identity or policy cannot be attested"
)

type monadWorkcellAttestationSource struct {
	sandboxID               string
	templateID              string
	buildID                 uuid.UUID
	metadata                map[string]string
	cpuCount                int64
	memoryMB                int64
	allowInternetAccess     *bool
	network                 *dbtypes.SandboxNetworkConfig
	autoPause               bool
	autoPauseFilesystemOnly bool
	autoResume              *dbtypes.SandboxAutoResumeConfig
}

func (a *APIStore) GetWellKnownMonadWorkcellAttestationsSandboxID(c *gin.Context, id api.SandboxID) {
	ctx := c.Request.Context()

	if a.config.MonadWorkcellAttestationCloud == "" {
		a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

		return
	}

	sandboxID, err := utils.ShortID(id)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	team := auth.MustGetTeamInfo(c).Team
	var source monadWorkcellAttestationSource

	sbx, runningErr := a.orchestrator.GetSandbox(ctx, team.ID, sandboxID)
	switch {
	case runningErr == nil:
		if sbx.TeamID != team.ID {
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

			return
		}
		if sbx.State != sandboxtypes.StateRunning {
			a.sendAPIStoreError(c, http.StatusConflict, monadAttestationConflictError)

			return
		}
		sourceBuildID, parseErr := monadRunningSourceBuildID(sbx)
		if parseErr != nil {
			telemetry.ReportError(ctx, "Monad workcell attestation rejected resumed sandbox image metadata", parseErr, telemetry.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusConflict, monadAttestationConflictError)

			return
		}

		source = monadWorkcellAttestationSource{
			sandboxID:               sbx.SandboxID,
			templateID:              sbx.BaseTemplateID,
			buildID:                 sourceBuildID,
			metadata:                sbx.Metadata,
			cpuCount:                sbx.VCpu,
			memoryMB:                sbx.RamMB,
			allowInternetAccess:     sbx.AllowInternetAccess,
			network:                 sbx.Network,
			autoPause:               sbx.AutoPause,
			autoPauseFilesystemOnly: sbx.AutoPauseFilesystemOnly,
			autoResume:              sbx.AutoResume,
		}
	case !errors.Is(runningErr, sandboxtypes.ErrNotFound):
		telemetry.ReportCriticalError(ctx, "loading running sandbox for Monad workcell attestation", runningErr)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error attesting sandbox")

		return
	default:
		lastSnapshot, snapshotErr := a.snapshotCache.Get(ctx, sandboxID)
		if snapshotErr != nil {
			if !errors.Is(snapshotErr, snapshotcache.ErrSnapshotNotFound) {
				telemetry.ReportCriticalError(ctx, "loading paused sandbox for Monad workcell attestation", snapshotErr)
				a.sendAPIStoreError(c, http.StatusInternalServerError, "Error attesting sandbox")

				return
			}

			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

			return
		}
		if lastSnapshot.Snapshot.TeamID != team.ID {
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(id))

			return
		}

		metadata := map[string]string(lastSnapshot.Snapshot.Metadata)
		sourceBuildID, parseErr := uuid.Parse(metadata[monadMetadataImageID])
		if parseErr != nil {
			telemetry.ReportError(ctx, "Monad workcell attestation rejected paused sandbox image metadata", parseErr, telemetry.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusConflict, monadAttestationConflictError)

			return
		}

		var network *dbtypes.SandboxNetworkConfig
		var autoResume *dbtypes.SandboxAutoResumeConfig
		var autoPauseFilesystemOnly bool
		if lastSnapshot.Snapshot.Config != nil {
			network = lastSnapshot.Snapshot.Config.Network
			autoResume = lastSnapshot.Snapshot.Config.AutoResume
			autoPauseFilesystemOnly = lastSnapshot.Snapshot.Config.AutoPauseFilesystemOnly
		}

		source = monadWorkcellAttestationSource{
			sandboxID:  lastSnapshot.Snapshot.SandboxID,
			templateID: lastSnapshot.Snapshot.BaseEnvID,
			// A pause creates a snapshot build. Monad's immutable workcell image
			// remains the source build in reserved metadata; the DB check below
			// proves it is assigned to the authoritative base template.
			buildID:                 sourceBuildID,
			metadata:                metadata,
			cpuCount:                lastSnapshot.EnvBuild.Vcpu,
			memoryMB:                lastSnapshot.EnvBuild.RamMb,
			allowInternetAccess:     lastSnapshot.Snapshot.AllowInternetAccess,
			network:                 network,
			autoPause:               lastSnapshot.Snapshot.AutoPause,
			autoPauseFilesystemOnly: autoPauseFilesystemOnly,
			autoResume:              autoResume,
		}
	}

	if err := a.verifyMonadTemplateBuild(c, source, team.ID); err != nil {
		return
	}

	attestation, err := buildMonadWorkcellAttestation(
		source,
		a.config.MonadWorkcellAttestationCloud,
		a.config.MonadWorkcellAttestationRegion,
	)
	if err != nil {
		telemetry.ReportError(ctx, "Monad workcell attestation rejected sandbox state", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusConflict, monadAttestationConflictError)

		return
	}

	c.JSON(http.StatusOK, attestation)
}

func monadRunningSourceBuildID(sbx sandboxtypes.Sandbox) (uuid.UUID, error) {
	// A resumed sandbox executes from the pause snapshot's transient build, while
	// BaseTemplateID and Monad's reserved image metadata continue to identify the
	// immutable source template build. The paused-sandbox path below uses the
	// same persisted source identity. Direct creates must still attest the actual
	// running build so callers cannot substitute metadata before the first pause.
	if sbx.TemplateID == sbx.BaseTemplateID {
		return sbx.BuildID, nil
	}

	sourceBuildID, err := uuid.Parse(sbx.Metadata[monadMetadataImageID])
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid resumed sandbox source build metadata: %w", err)
	}

	return sourceBuildID, nil
}

func (a *APIStore) verifyMonadTemplateBuild(c *gin.Context, source monadWorkcellAttestationSource, teamID uuid.UUID) error {
	ctx := c.Request.Context()
	templateBuild, err := a.sqlcDB.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
		TemplateID: source.templateID,
		BuildID:    source.buildID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			telemetry.ReportError(ctx, "Monad workcell attestation template build not found", err, telemetry.WithSandboxID(source.sandboxID))
			a.sendAPIStoreError(c, http.StatusConflict, monadAttestationConflictError)

			return err
		}

		telemetry.ReportCriticalError(ctx, "loading template build for Monad workcell attestation", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error attesting sandbox")

		return err
	}

	if templateBuild.ActiveEnv.TeamID != teamID || templateBuild.EnvBuild.StatusGroup != dbtypes.BuildStatusGroupReady {
		err := fmt.Errorf(
			"template build ownership/readiness mismatch: template team %s, request team %s, status %s",
			templateBuild.ActiveEnv.TeamID,
			teamID,
			templateBuild.EnvBuild.StatusGroup,
		)
		telemetry.ReportError(ctx, "Monad workcell attestation rejected template build", err, telemetry.WithSandboxID(source.sandboxID))
		a.sendAPIStoreError(c, http.StatusConflict, monadAttestationConflictError)

		return err
	}

	return nil
}

func buildMonadWorkcellAttestation(source monadWorkcellAttestationSource, cloud, region string) (api.MonadWorkcellAttestation, error) {
	if cloud == "" || region == "" {
		return api.MonadWorkcellAttestation{}, errors.New("attestation placement is not configured")
	}
	if source.sandboxID == "" || source.templateID == "" || source.buildID == uuid.Nil {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox immutable identity is incomplete")
	}
	if source.metadata[monadMetadataProvider] != monadProviderE2B {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox provider metadata is not e2b")
	}
	if source.metadata[monadMetadataTemplateID] != source.templateID {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox template metadata does not match control-plane state")
	}
	if source.metadata[monadMetadataIdentityFidelity] != monadIdentityImageAttested {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox identity fidelity is not image-attested")
	}
	metadataBuildID, err := uuid.Parse(source.metadata[monadMetadataImageID])
	if err != nil || metadataBuildID != source.buildID {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox image metadata does not match control-plane build")
	}
	if source.metadata[monadMetadataPlacement] != region {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox placement metadata does not match configured region")
	}
	if source.cpuCount < 1 || source.memoryMB < 1 {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox resources are invalid")
	}
	if source.allowInternetAccess == nil {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox internet-access policy is not explicit")
	}
	if source.network == nil || source.network.Ingress == nil || source.network.Ingress.AllowPublicAccess == nil {
		return api.MonadWorkcellAttestation{}, errors.New("sandbox public-ingress policy is not explicit")
	}

	attestation := api.MonadWorkcellAttestation{
		Cloud:            cloud,
		IdentityFidelity: api.ImageAttested,
		ImageId:          source.buildID,
		Provider:         api.E2b,
		Region:           region,
		SandboxId:        source.sandboxID,
		SchemaVersion:    api.N1,
		TemplateId:       source.templateID,
	}
	attestation.Resources.CpuCount = source.cpuCount
	attestation.Resources.MemoryMb = source.memoryMB
	attestation.Network.AllowInternetAccess = *source.allowInternetAccess
	attestation.Network.AllowPublicTraffic = *source.network.Ingress.AllowPublicAccess
	if source.network.Egress != nil {
		if source.network.Egress.AllowedAddresses != nil {
			allowOut := append([]string(nil), source.network.Egress.AllowedAddresses...)
			attestation.Network.AllowOut = &allowOut
		}
		if source.network.Egress.DeniedAddresses != nil {
			denyOut := append([]string(nil), source.network.Egress.DeniedAddresses...)
			attestation.Network.DenyOut = &denyOut
		}
	}

	attestation.Lifecycle.AutoResume = source.autoResume != nil && source.autoResume.Policy == dbtypes.SandboxAutoResumeAny
	attestation.Lifecycle.OnTimeout = api.Kill
	if source.autoPause {
		attestation.Lifecycle.OnTimeout = api.Pause
		pauseFidelity := api.FilesystemAndMemory
		if source.autoPauseFilesystemOnly {
			pauseFidelity = api.FilesystemOnly
		}
		attestation.Lifecycle.PauseFidelity = &pauseFidelity
	}

	return attestation, nil
}
