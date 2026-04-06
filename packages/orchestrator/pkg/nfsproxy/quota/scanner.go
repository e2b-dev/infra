package quota

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	defaultScanTimeout   = 5 * time.Minute
	defaultPopTimeout    = 5 * time.Second
	defaultBackoffDelay  = 1 * time.Second
)

// VolumePathBuilder constructs filesystem paths for volumes.
type VolumePathBuilder interface {
	// BuildVolumePath returns the filesystem path for a volume.
	BuildVolumePath(volumeType string, teamID, volumeID uuid.UUID) string
}

// Scanner processes dirty volumes and measures their disk usage.
type Scanner struct {
	tracker     *Tracker
	pathBuilder VolumePathBuilder
	logger      *zap.Logger

	// volumeType is used when building paths (e.g., "default")
	volumeType string
}

// NewScanner creates a new volume scanner.
func NewScanner(
	tracker *Tracker,
	pathBuilder VolumePathBuilder,
	volumeType string,
	logger *zap.Logger,
) *Scanner {
	return &Scanner{
		tracker:     tracker,
		pathBuilder: pathBuilder,
		volumeType:  volumeType,
		logger:      logger,
	}
}

// Run starts the scanner loop. It processes dirty volumes until the context is cancelled.
func (s *Scanner) Run(ctx context.Context) error {
	s.logger.Info("starting volume quota scanner")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("volume quota scanner stopped")
			return ctx.Err()
		default:
			if err := s.processNext(ctx); err != nil {
				s.logger.Warn("error processing dirty volume", zap.Error(err))
				// Backoff on error
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(defaultBackoffDelay):
				}
			}
		}
	}
}

// processNext pops and processes the next dirty volume.
func (s *Scanner) processNext(ctx context.Context) error {
	vol, found, err := s.tracker.BlockingPopDirtyVolume(ctx, defaultPopTimeout)
	if err != nil {
		return fmt.Errorf("pop dirty volume: %w", err)
	}
	if !found {
		return nil // No work, loop will continue
	}

	return s.scanVolume(ctx, vol)
}

// scanVolume measures disk usage for a volume and updates quota status.
func (s *Scanner) scanVolume(ctx context.Context, vol VolumeInfo) error {
	volPath := s.pathBuilder.BuildVolumePath(s.volumeType, vol.TeamID, vol.VolumeID)

	s.logger.Debug("scanning volume",
		zap.String("team_id", vol.TeamID.String()),
		zap.String("volume_id", vol.VolumeID.String()),
		zap.String("path", volPath))

	usage, err := s.measureDiskUsage(ctx, volPath)
	if err != nil {
		// Volume might be deleted - log and continue
		s.logger.Warn("failed to measure disk usage",
			zap.String("volume", vol.String()),
			zap.String("path", volPath),
			zap.Error(err))
		return nil // Don't return error - this is expected for deleted volumes
	}

	// Update usage in Redis
	if err := s.tracker.SetUsage(ctx, vol, usage); err != nil {
		return fmt.Errorf("set usage for %s: %w", vol.String(), err)
	}

	// Check quota and update blocked status
	quota, err := s.tracker.GetQuota(ctx, vol)
	if err != nil {
		// No quota set - not blocked
		if err := s.tracker.SetBlocked(ctx, vol, false); err != nil {
			return fmt.Errorf("set blocked for %s: %w", vol.String(), err)
		}
		return nil
	}

	blocked := usage >= quota
	if err := s.tracker.SetBlocked(ctx, vol, blocked); err != nil {
		return fmt.Errorf("set blocked for %s: %w", vol.String(), err)
	}

	s.logger.Debug("volume scan complete",
		zap.String("volume", vol.String()),
		zap.Int64("usage_bytes", usage),
		zap.Int64("quota_bytes", quota),
		zap.Bool("blocked", blocked))

	return nil
}

// measureDiskUsage uses `du -sb` to measure the total size of a directory.
func (s *Scanner) measureDiskUsage(ctx context.Context, path string) (int64, error) {
	// Use a timeout for the du command
	ctx, cancel := context.WithTimeout(ctx, defaultScanTimeout)
	defer cancel()

	// du -sb: summarize, bytes
	cmd := exec.CommandContext(ctx, "du", "-sb", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("du command failed: %w", err)
	}

	// Output format: "12345\t/path/to/dir\n"
	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return 0, fmt.Errorf("unexpected du output: %s", string(output))
	}

	usage, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse du output: %w", err)
	}

	return usage, nil
}

// SimplePathBuilder is a basic implementation of VolumePathBuilder.
type SimplePathBuilder struct {
	// MountPoints maps volume type to base mount path
	// e.g., {"default": "/volumes", "cache": "/cache_volumes"}
	MountPoints map[string]string
}

// BuildVolumePath constructs the path for a volume.
func (b *SimplePathBuilder) BuildVolumePath(volumeType string, teamID, volumeID uuid.UUID) string {
	basePath, ok := b.MountPoints[volumeType]
	if !ok {
		basePath = b.MountPoints["default"]
	}

	return filepath.Join(basePath, fmt.Sprintf("team-%s", teamID.String()), fmt.Sprintf("vol-%s", volumeID.String()))
}
