package server

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	migrationTimeout = 5 * time.Minute
	migrationPeerTTL = 30 * time.Minute
)

func (s *Server) InitMigration(ctx context.Context, req *orchestrator.MigrationInitRequest) (*orchestrator.MigrationInitResponse, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, migrationTimeout, fmt.Errorf("migration timed out"))
	defer cancel()

	ctx, span := tracer.Start(ctx, "migration-init")
	defer span.End()
	span.SetAttributes(telemetry.WithSandboxID(req.GetSandboxId()))

	sbx, err := s.acquireSandboxForSnapshot(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}

	sbxlogger.E(sbx).Info(ctx, "Starting live migration", zap.String("dest", req.GetDestAddress()))
	defer s.stopSandboxAsync(context.WithoutCancel(ctx), sbx)

	buildID := uuid.New().String()

	res, err := s.snapshotAndCacheSandbox(ctx, sbx, buildID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "snapshot for migration: %s", err)
	}
	_ = res // no GCS upload — snapshot exists only for P2P transfer

	if err := s.peerRegistry.Register(ctx, buildID, migrationPeerTTL); err != nil {
		logger.L().Warn(ctx, "migration: peer register failed", zap.String("build_id", buildID), zap.Error(err))
	}

	destConn, err := grpc.NewClient(req.GetDestAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "connect to destination: %s", err)
	}
	defer destConn.Close()

	destClient := orchestrator.NewMigrationServiceClient(destConn)
	sourceAddr := fmt.Sprintf("%s:%d", s.config.NodeIP, s.config.GRPCPort)

	sbxConfig := sbx.APIStoredConfig
	if sbxConfig == nil {
		sbxConfig = &orchestrator.SandboxConfig{
			TemplateId:         sbx.Runtime.TemplateID,
			BuildId:            buildID,
			KernelVersion:      sbx.Config.FirecrackerConfig.KernelVersion,
			FirecrackerVersion: sbx.Config.FirecrackerConfig.FirecrackerVersion,
			HugePages:          sbx.Config.HugePages,
			SandboxId:          sbx.Runtime.SandboxID,
			EnvVars:            sbx.Config.Envd.Vars,
			Vcpu:               sbx.Config.Vcpu,
			RamMb:              sbx.Config.RamMB,
			TeamId:             sbx.Runtime.TeamID,
			TotalDiskSizeMb:    sbx.Config.TotalDiskSizeMB,
			BaseTemplateId:     sbx.Config.BaseTemplateID,
			ExecutionId:        sbx.Runtime.ExecutionID,
			EnvdAccessToken:    sbx.Config.Envd.AccessToken,
			EnvdVersion:        sbx.Config.Envd.Version,
		}
	}
	// Override with migration build ID
	sbxConfig.BuildId = buildID
	sbxConfig.Snapshot = true

	receiveResp, err := destClient.ReceiveMigration(ctx, &orchestrator.MigrationReceiveRequest{
		BuildId:       buildID,
		SourceAddress: sourceAddr,
		Sandbox:       sbxConfig,
		StartTime:     timestamppb.New(sbx.GetStartedAt()),
		EndTime:       timestamppb.New(sbx.GetEndAt()),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "destination receive: %s", err)
	}

	sbxlogger.E(sbx).Info(ctx, "Migration completed",
		zap.String("dest", req.GetDestAddress()),
		zap.String("dest_client_id", receiveResp.GetClientId()),
		zap.String("build_id", buildID))

	return &orchestrator.MigrationInitResponse{BuildId: buildID}, nil
}

func (s *Server) ReceiveMigration(ctx context.Context, req *orchestrator.MigrationReceiveRequest) (*orchestrator.MigrationReceiveResponse, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, migrationTimeout, fmt.Errorf("migration receive timed out"))
	defer cancel()

	ctx, span := tracer.Start(ctx, "migration-receive")
	defer span.End()

	cfg := req.GetSandbox()
	sandboxID := cfg.GetSandboxId()
	span.SetAttributes(telemetry.WithSandboxID(sandboxID))

	logger.L().Info(ctx, "Receiving migration",
		zap.String("sandbox_id", sandboxID),
		zap.String("build_id", req.GetBuildId()),
		zap.String("source", req.GetSourceAddress()))

	if err := s.waitForAcquire(ctx); err != nil {
		return nil, err
	}
	defer s.startingSandboxes.Release(1)

	// Force peer routing — the snapshot only exists on the source peer.
	tmpl, err := s.templateCache.GetTemplateWithPeerRouting(ctx, req.GetBuildId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get template: %s", err)
	}

	network := &orchestrator.SandboxNetworkConfig{}
	allowInternet := s.config.AllowSandboxInternet
	if cfg.AllowInternetAccess != nil {
		allowInternet = cfg.GetAllowInternetAccess()
	}
	if !allowInternet {
		network.Egress = &orchestrator.SandboxNetworkEgressConfig{
			DeniedCidrs: []string{"0.0.0.0/0"},
		}
	}

	sbx, err := s.sandboxFactory.ResumeSandbox(
		ctx,
		tmpl,
		sandbox.Config{
			BaseTemplateID:  cfg.GetBaseTemplateId(),
			Vcpu:            cfg.GetVcpu(),
			RamMB:           cfg.GetRamMb(),
			TotalDiskSizeMB: cfg.GetTotalDiskSizeMb(),
			HugePages:       cfg.GetHugePages(),
			Network:         network,
			Envd: sandbox.EnvdMetadata{
				Version:     cfg.GetEnvdVersion(),
				AccessToken: cfg.EnvdAccessToken,
				Vars:        cfg.GetEnvVars(),
			},
			FirecrackerConfig: fc.Config{
				KernelVersion:      cfg.GetKernelVersion(),
				FirecrackerVersion: cfg.GetFirecrackerVersion(),
			},
		},
		sandbox.RuntimeMetadata{
			TemplateID:  cfg.GetTemplateId(),
			SandboxID:   cfg.GetSandboxId(),
			ExecutionID: cfg.GetExecutionId(),
			TeamID:      cfg.GetTeamId(),
		},
		req.GetStartTime().AsTime(),
		req.GetEndTime().AsTime(),
		cfg,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resume sandbox: %s", err)
	}

	s.setupSandboxLifecycle(ctx, sbx)

	// Background: pull all chunks from source, then notify source to clean up.
	go s.pullAllChunksAndComplete(context.WithoutCancel(ctx), tmpl, req.GetBuildId(), req.GetSourceAddress())

	return &orchestrator.MigrationReceiveResponse{ClientId: s.info.ClientId}, nil
}

func (s *Server) CompleteMigration(ctx context.Context, req *orchestrator.MigrationCompleteRequest) (*orchestrator.MigrationCompleteResponse, error) {
	ctx, span := tracer.Start(ctx, "migration-complete")
	defer span.End()

	buildID := req.GetBuildId()
	logger.L().Info(ctx, "Migration complete, cleaning up", zap.String("build_id", buildID))

	if err := s.peerRegistry.Unregister(ctx, buildID); err != nil {
		logger.L().Warn(ctx, "migration: peer unregister failed", zap.String("build_id", buildID), zap.Error(err))
	}
	s.templateCache.Invalidate(buildID)

	return &orchestrator.MigrationCompleteResponse{}, nil
}

// pullAllChunksAndComplete reads every block of memfile+rootfs to populate the
// local cache, then calls CompleteMigration on the source.
func (s *Server) pullAllChunksAndComplete(ctx context.Context, tmpl template.Template, buildID, sourceAddr string) {
	logger.L().Info(ctx, "migration: pulling all chunks", zap.String("build_id", buildID))

	if memfile, err := tmpl.Memfile(ctx); err == nil {
		pullAllBlocks(ctx, memfile, "memfile")
	}
	if rootfs, err := tmpl.Rootfs(); err == nil {
		pullAllBlocks(ctx, rootfs, "rootfs")
	}

	logger.L().Info(ctx, "migration: chunks pulled, notifying source", zap.String("build_id", buildID))

	conn, err := grpc.NewClient(sourceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.L().Warn(ctx, "migration: connect to source for completion failed", zap.Error(err))

		return
	}
	defer conn.Close()

	completeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := orchestrator.NewMigrationServiceClient(conn).CompleteMigration(completeCtx, &orchestrator.MigrationCompleteRequest{BuildId: buildID}); err != nil {
		logger.L().Warn(ctx, "migration: CompleteMigration failed (source will clean up via TTL)", zap.Error(err))
	}
}

func pullAllBlocks(ctx context.Context, dev block.ReadonlyDevice, name string) {
	size, err := dev.Size(ctx)
	if err != nil || size <= 0 {
		return
	}

	blockSize := dev.BlockSize()
	for off := int64(0); off < size && ctx.Err() == nil; off += blockSize {
		length := blockSize
		if off+length > size {
			length = size - off
		}
		dev.Slice(ctx, off, length) //nolint:errcheck // best-effort
	}
}
