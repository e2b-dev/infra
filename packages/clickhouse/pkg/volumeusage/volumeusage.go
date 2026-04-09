package volumeusage

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// VolumeUsageSnapshot represents a point-in-time snapshot of volume usage.
// These snapshots are used for billing and quota enforcement auditing.
type VolumeUsageSnapshot struct {
	Timestamp  time.Time `ch:"timestamp"`
	TeamID     uuid.UUID `ch:"team_id"`
	VolumeID   uuid.UUID `ch:"volume_id"`
	UsageBytes int64     `ch:"usage_bytes"`
	QuotaBytes int64     `ch:"quota_bytes"` // 0 if no quota set
	IsBlocked  bool      `ch:"is_blocked"`
}

// Delivery is the interface for delivering volume usage snapshots to storage backend.
type Delivery interface {
	Push(snapshot VolumeUsageSnapshot) error
	Close(ctx context.Context) error
}

// noopDelivery is a Delivery that discards all snapshots.
type noopDelivery struct{}

var _ Delivery = (*noopDelivery)(nil)

// NewNoopDelivery returns a Delivery that silently discards all snapshots.
func NewNoopDelivery() Delivery {
	return &noopDelivery{}
}

func (d *noopDelivery) Push(_ VolumeUsageSnapshot) error { return nil }
func (d *noopDelivery) Close(_ context.Context) error    { return nil }
