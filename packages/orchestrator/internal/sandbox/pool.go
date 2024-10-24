package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"

	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/trace"
)

type NetworkSlotPool struct {
	newSlots chan IPSlot
}

func NewNetworkSlotPool(size int) *NetworkSlotPool {
	newSlots := make(chan IPSlot, size-1)

	return &NetworkSlotPool{
		newSlots: newSlots,
	}
}

func (p *NetworkSlotPool) Get(ctx context.Context, tracer trace.Tracer) (IPSlot, error) {
	childCtx, networkSpan := tracer.Start(ctx, "get-network-slot")
	defer networkSpan.End()

	select {
	case <-childCtx.Done():
		return IPSlot{}, childCtx.Err()
	case newSlot := <-p.newSlots:
		return newSlot, nil
	}
}

func (p *NetworkSlotPool) Start(ctx context.Context, consulClient *consul.Client) error {
	defer close(p.newSlots)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			ips, err := NewSlot(consulClient)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			err = ips.CreateNetwork()
			if err != nil {
				ips.Release(consulClient)

				fmt.Fprintf(os.Stderr, "[network slot pool]: failed to create network: %v\n", err)

				continue
			}

			p.newSlots <- *ips
		}
	}
}

func cleanupSlot(consul *consul.Client, slot IPSlot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.SlotIdx, err))
	}

	err = slot.Release(consul)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.SlotIdx, err))
	}

	return errors.Join(errs...)
}

func (p *NetworkSlotPool) Release(consul *consul.Client, slot IPSlot) error {
	return cleanupSlot(consul, slot)
}

func (p *NetworkSlotPool) Close(consul *consul.Client) error {
	var errs []error

	for slot := range p.newSlots {
		err := cleanupSlot(consul, slot)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
