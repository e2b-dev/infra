package instance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Instance struct {
	Files *InstanceFiles
	Slot  *IPSlot
	FC    *FC

	config *InstanceConfig

	EnvID string
}

var httpClient = http.Client{
	Timeout: 5 * time.Second,
}

type InstanceConfig struct {
	EnvID                 string
	AllocID               string
	NodeID                string
	ConsulToken           string
	EnvsDisk              string
	InstanceID            string
	LogsProxyAddress      string
	TraceID               string
	TeamID                string
	KernelVersion         string
	KernelMountDir        string
	KernelsDir            string
	KernelName            string
	FirecrackerBinaryPath string
	UFFDBinaryPath        string
	HugePages             bool
}

func NewInstance(
	ctx context.Context,
	tracer trace.Tracer,
	config *InstanceConfig,
	dns *DNS,
) (*Instance, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-instance")
	defer childSpan.End()

	telemetry.SetAttributes(childCtx,
		attribute.String("alloc.id", config.AllocID),
	)

	// Get slot from Consul KV
	ips, err := NewSlot(
		childCtx,
		tracer,
		config.NodeID,
		config.InstanceID,
		config.ConsulToken,
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to get IP slot: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "reserved ip slot")

	defer func() {
		if err != nil {
			slotErr := ips.Release(childCtx, tracer)
			if slotErr != nil {
				errMsg := fmt.Errorf("error removing network namespace after failed instance start: %w", slotErr)
				telemetry.ReportError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "released ip slot")
			}
		}
	}()

	defer func() {
		if err != nil {
			ntErr := ips.RemoveNetwork(childCtx, tracer, dns)
			if ntErr != nil {
				errMsg := fmt.Errorf("error removing network namespace after failed instance start: %w", ntErr)
				telemetry.ReportError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "removed network namespace")
			}
		}
	}()

	err = ips.CreateNetwork(childCtx, tracer, dns)
	if err != nil {
		errMsg := fmt.Errorf("failed to create namespaces: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "created network")

	fsEnv, err := newInstanceFiles(
		childCtx,
		tracer,
		ips,
		config.EnvID,
		config.EnvsDisk,
		config.KernelVersion,
		config.KernelsDir,
		config.KernelMountDir,
		config.KernelName,
		config.FirecrackerBinaryPath,
		config.UFFDBinaryPath,
		config.HugePages,
	)
	if err != nil {
		errMsg := fmt.Errorf("failed to create env for FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "created env for FC")

	defer func() {
		if err != nil {
			envErr := fsEnv.Cleanup(childCtx, tracer)
			if envErr != nil {
				errMsg := fmt.Errorf("error deleting env after failed fc start: %w", err)
				telemetry.ReportCriticalError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "deleted env")
			}
		}
	}()

	fc := NewFC(
		childCtx,
		tracer,
		ips,
		fsEnv,
		&MmdsMetadata{
			InstanceID: config.InstanceID,
			EnvID:      config.EnvID,
			Address:    config.LogsProxyAddress,
			TraceID:    config.TraceID,
			TeamID:     config.TeamID,
		},
	)

	err = fc.Start(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to start FC: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "initialized FC")

	instance := &Instance{
		EnvID: config.EnvID,
		Files: fsEnv,
		Slot:  ips,
		FC:    fc,

		config: config,
	}

	telemetry.ReportEvent(childCtx, "ensuring clock sync")
	go func() {
		context := context.Background()

		clockErr := instance.EnsureClockSync(context)
		if clockErr != nil {
			telemetry.ReportError(context, fmt.Errorf("failed to sync clock: %w", clockErr))
		} else {
			telemetry.ReportEvent(context, "clock synced")
		}
	}()

	return instance, nil
}

func (i *Instance) syncClock(ctx context.Context) error {
	address := fmt.Sprintf("http://%s:%d/sync", i.Slot.HostSnapshotIP(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "POST", address, nil)
	if err != nil {
		return err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}

	defer response.Body.Close()

	return nil
}

func (i *Instance) EnsureClockSync(ctx context.Context) error {
syncLoop:
	for {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := i.syncClock(ctx)
			if err != nil {
				telemetry.ReportError(ctx, fmt.Errorf("error syncing clock: %w", err))
				continue
			}
			break syncLoop
		}
	}

	return nil
}

func (i *Instance) CleanupAfterFCStop(
	ctx context.Context,
	tracer trace.Tracer,
	dns *DNS,
) {
	childCtx, childSpan := tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	err := i.Slot.RemoveNetwork(childCtx, tracer, dns)
	if err != nil {
		errMsg := fmt.Errorf("cannot remove network when destroying task: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed network")
	}

	err = i.Files.Cleanup(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to delete instance files: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "deleted instance files")
	}

	err = i.Slot.Release(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("failed to release slot: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "released slot")
	}
}
