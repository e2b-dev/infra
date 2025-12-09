package fc

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
)

func (p *Process) Memory(ctx context.Context) (*memory.View, error) {
	pid, err := p.Pid()
	if err != nil {
		return nil, fmt.Errorf("failed to get process pid: %w", err)
	}

	info, err := p.client.instanceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance info: %w", err)
	}

	mapping, err := memory.NewMappingFromFCInfo(info.MemoryRegions)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory mapping: %w", err)
	}

	view, err := memory.NewView(pid, mapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory view: %w", err)
	}

	return view, nil
}
