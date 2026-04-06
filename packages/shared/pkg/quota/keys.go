// Package quota provides shared quota key definitions and utilities
// for both the API and orchestrator services.
package quota

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// Redis key prefixes for quota management
const (
	DirtyVolumesKey  = "quota:dirty_volumes"
	VolumeUsageKey   = "quota:volume:usage"
	VolumeBlockedKey = "quota:volume:blocked"
	VolumeQuotaKey   = "quota:volume:quota"
)

// VolumeInfo identifies a volume for quota tracking.
type VolumeInfo struct {
	TeamID   uuid.UUID
	VolumeID uuid.UUID
}

// String returns the canonical string representation for Redis keys.
func (v VolumeInfo) String() string {
	return fmt.Sprintf("%s/%s", v.TeamID.String(), v.VolumeID.String())
}

// ParseVolumeInfo parses a "teamID/volumeID" string into a VolumeInfo.
func ParseVolumeInfo(s string) (VolumeInfo, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return VolumeInfo{}, fmt.Errorf("invalid format: expected 'teamID/volumeID', got %q", s)
	}

	teamID, err := uuid.Parse(parts[0])
	if err != nil {
		return VolumeInfo{}, fmt.Errorf("invalid team ID: %w", err)
	}

	volumeID, err := uuid.Parse(parts[1])
	if err != nil {
		return VolumeInfo{}, fmt.Errorf("invalid volume ID: %w", err)
	}

	return VolumeInfo{TeamID: teamID, VolumeID: volumeID}, nil
}

// VolumeQuotaInfo contains quota information for a volume.
type VolumeQuotaInfo struct {
	VolumeInfo
	UsageBytes int64 `json:"usage_bytes"`
	QuotaBytes int64 `json:"quota_bytes"` // 0 means no quota set
	IsBlocked  bool  `json:"is_blocked"`
}

// Client provides methods for interacting with quota data in Redis.
type Client struct {
	redis redis.UniversalClient
}

// NewClient creates a new quota client.
func NewClient(redisClient redis.UniversalClient) *Client {
	return &Client{redis: redisClient}
}

// GetVolumeQuota gets the quota information for a volume.
func (c *Client) GetVolumeQuota(ctx context.Context, vol VolumeInfo) (VolumeQuotaInfo, error) {
	volStr := vol.String()

	pipe := c.redis.Pipeline()
	usageCmd := pipe.Get(ctx, redis_utils.CreateKey(VolumeUsageKey, volStr))
	quotaCmd := pipe.Get(ctx, redis_utils.CreateKey(VolumeQuotaKey, volStr))
	blockedCmd := pipe.Get(ctx, redis_utils.CreateKey(VolumeBlockedKey, volStr))
	_, _ = pipe.Exec(ctx) // Ignore exec error, check individual commands

	info := VolumeQuotaInfo{VolumeInfo: vol}

	// Get usage (may not exist)
	if usage, err := usageCmd.Int64(); err == nil {
		info.UsageBytes = usage
	}

	// Get quota (may not exist - 0 means unlimited)
	if quota, err := quotaCmd.Int64(); err == nil {
		info.QuotaBytes = quota
	}

	// Get blocked status
	if blocked, err := blockedCmd.Bool(); err == nil {
		info.IsBlocked = blocked
	}

	return info, nil
}

// SetVolumeQuota sets the quota limit for a volume.
func (c *Client) SetVolumeQuota(ctx context.Context, vol VolumeInfo, quotaBytes int64) error {
	key := redis_utils.CreateKey(VolumeQuotaKey, vol.String())
	return c.redis.Set(ctx, key, quotaBytes, 0).Err()
}

// RemoveVolumeQuota removes the quota limit for a volume (makes it unlimited).
func (c *Client) RemoveVolumeQuota(ctx context.Context, vol VolumeInfo) error {
	key := redis_utils.CreateKey(VolumeQuotaKey, vol.String())
	return c.redis.Del(ctx, key).Err()
}

// GetTeamVolumes returns all volumes for a team with their quota info.
func (c *Client) GetTeamVolumes(ctx context.Context, teamID uuid.UUID) ([]VolumeQuotaInfo, error) {
	// Scan for all usage keys for this team
	pattern := redis_utils.CreateKey(VolumeUsageKey, teamID.String(), "*")
	iter := c.redis.Scan(ctx, 0, pattern, 1000).Iterator()

	var volumes []VolumeQuotaInfo
	for iter.Next(ctx) {
		key := iter.Val()
		// Extract volume info from key: quota:volume:usage:{teamID}/{volumeID}
		volStr := key[len(VolumeUsageKey)+1:] // +1 for the separator

		vol, err := ParseVolumeInfo(volStr)
		if err != nil {
			continue
		}

		info, err := c.GetVolumeQuota(ctx, vol)
		if err != nil {
			continue
		}

		volumes = append(volumes, info)
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("error scanning team volumes: %w", err)
	}

	return volumes, nil
}

// GetTeamTotalUsage returns the total usage across all volumes for a team.
func (c *Client) GetTeamTotalUsage(ctx context.Context, teamID uuid.UUID) (int64, error) {
	volumes, err := c.GetTeamVolumes(ctx, teamID)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, v := range volumes {
		total += v.UsageBytes
	}

	return total, nil
}
