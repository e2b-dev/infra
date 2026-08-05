package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

const (
	PlannedDensity         = 2
	EmergencyDensity       = 3
	MinimumWorkerHosts     = 2
	MaximumWorkerHosts     = 15
	SpareWorkerHosts       = 1
	ScaleInWindow          = 15 * time.Minute
	MaximumSnapshotAge     = 90 * time.Second
	MaximumFutureSkew      = 30 * time.Second
	MaximumObservationGap  = 75 * time.Second
	MaximumReportedCount   = 1_000_000
	FixedControlPlaneNodes = 6
	MaximumDurableSessions = 100
	InvitedBetaActiveLimit = 25
	InvitedBetaQueuedLimit = 75
	EmergencyFleetCapacity = MaximumWorkerHosts * EmergencyDensity
)

var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// Overview is the complete narrow envelope returned by TAMS
// GET /v1/ops/capacity. Capacity is decoded strictly so a changed scaling
// contract fails closed.
type Overview struct {
	GeneratedAt time.Time `json:"generated_at"`
	Capacity    Capacity  `json:"capacity"`
}

type Capacity struct {
	Revision                      string     `json:"revision"`
	DurableSessions               int        `json:"durable_sessions"`
	ActiveWorkcells               int        `json:"active_workcells"`
	ParkedWorkcells               int        `json:"parked_workcells"`
	BootingWorkcells              int        `json:"booting_workcells"`
	DrainingWorkcells             int        `json:"draining_workcells"`
	WarmTarget                    *int       `json:"warm_target"`
	CountedWorkcells              int        `json:"counted_workcells"`
	ActiveLimit                   int        `json:"active_limit"`
	QueuedLimit                   int        `json:"queued_limit"`
	PlannedDensityPerWorker       int        `json:"planned_density_per_worker"`
	HardEmergencyDensityPerWorker int        `json:"hard_emergency_density_per_worker"`
	DesiredWorkerHosts            int        `json:"desired_worker_hosts"`
	WorkerHostBounds              HostBounds `json:"worker_host_bounds"`
	FixedControlPlaneNodes        int        `json:"fixed_control_plane_nodes"`
}

type HostBounds struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type Fleet struct {
	ActualHosts   int
	DrainingHosts int
}

type Direction string

const (
	DirectionHold     Direction = "hold"
	DirectionScaleOut Direction = "scale_out"
	DirectionScaleIn  Direction = "scale_in"
)

type Decision struct {
	Mode                      string     `json:"mode"`
	Revision                  string     `json:"revision"`
	ObservedAt                time.Time  `json:"observed_at"`
	SnapshotAgeSeconds        float64    `json:"snapshot_age_seconds"`
	DurableSessions           int        `json:"durable_sessions"`
	ActiveWorkcells           int        `json:"active_workcells"`
	BootingWorkcells          int        `json:"booting_workcells"`
	DrainingWorkcells         int        `json:"draining_workcells"`
	ParkedWorkcells           int        `json:"parked_workcells"`
	WarmTarget                int        `json:"warm_target"`
	RequiredWorkcells         int        `json:"required_workcells"`
	DesiredWorkerHosts        int        `json:"desired_worker_hosts"`
	ActualWorkerHosts         int        `json:"actual_worker_hosts"`
	DrainingWorkerHosts       int        `json:"draining_worker_hosts"`
	FixedControlPlaneNodes    int        `json:"fixed_control_plane_nodes"`
	Direction                 Direction  `json:"direction"`
	LowDemandSince            *time.Time `json:"low_demand_since,omitempty"`
	LowDemandSeconds          float64    `json:"low_demand_seconds"`
	ScaleInWindowElapsed      bool       `json:"scale_in_window_elapsed"`
	RequiresDrainVerification bool       `json:"requires_drain_verification"`
	MutationAllowed           bool       `json:"mutation_allowed"`
	Reason                    string     `json:"reason"`
}

func DecodeOverview(reader io.Reader) (Overview, error) {
	limited := io.LimitReader(reader, (1<<20)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Overview{}, fmt.Errorf("read TAMS capacity: %w", err)
	}
	if len(data) > 1<<20 {
		return Overview{}, errors.New("TAMS capacity response exceeds 1 MiB")
	}

	var envelope struct {
		GeneratedAt json.RawMessage `json:"generated_at"`
		Capacity    json.RawMessage `json:"capacity"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Overview{}, fmt.Errorf("decode TAMS capacity envelope: %w", err)
	}
	if len(envelope.GeneratedAt) == 0 || len(envelope.Capacity) == 0 {
		return Overview{}, errors.New("TAMS capacity response requires generated_at and capacity")
	}

	var observedAt time.Time
	if err := json.Unmarshal(envelope.GeneratedAt, &observedAt); err != nil {
		return Overview{}, fmt.Errorf("decode TAMS capacity generated_at: %w", err)
	}

	var capacity Capacity
	var capacityFields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Capacity, &capacityFields); err != nil {
		return Overview{}, fmt.Errorf("decode TAMS capacity field map: %w", err)
	}
	requiredFields := []string{
		"revision", "durable_sessions", "active_workcells", "parked_workcells",
		"booting_workcells", "draining_workcells", "warm_target", "counted_workcells",
		"active_limit", "queued_limit", "planned_density_per_worker",
		"hard_emergency_density_per_worker", "desired_worker_hosts", "worker_host_bounds",
		"fixed_control_plane_nodes",
	}
	for _, field := range requiredFields {
		if _, exists := capacityFields[field]; !exists {
			return Overview{}, fmt.Errorf("TAMS capacity is missing required field %q", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Capacity))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&capacity); err != nil {
		return Overview{}, fmt.Errorf("decode strict TAMS capacity snapshot: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Overview{}, errors.New("TAMS capacity contains trailing JSON")
	}

	return Overview{GeneratedAt: observedAt, Capacity: capacity}, nil
}

func RequiredWorkcells(capacity Capacity) int {
	warm := 0
	if capacity.WarmTarget != nil {
		warm = *capacity.WarmTarget
	}
	parkedOrWarm := max(capacity.ParkedWorkcells, warm)

	return capacity.ActiveWorkcells + capacity.BootingWorkcells + capacity.DrainingWorkcells + parkedOrWarm
}

func DesiredHosts(required int) int {
	return min(MaximumWorkerHosts, max(MinimumWorkerHosts, (required+PlannedDensity-1)/PlannedDensity+SpareWorkerHosts))
}

func capacityDigest(capacity Capacity) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(capacity)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode capacity digest: %w", err)
	}

	return sha256.Sum256(encoded), nil
}
