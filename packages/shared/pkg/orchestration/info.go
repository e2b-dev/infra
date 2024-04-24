package orchestration

import (
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type Info struct {
	Config    *orchestrator.SandboxConfig `json:"config,omitempty"`
	TraceID   string                      `json:"traceID,omitempty"`
	SlotIdx   int                         `json:"slotIdx,omitempty"`
	FcPid     int                         `json:"fcPid,omitempty"`
	UffdPid   *int                        `json:"uffdPid,omitempty"`
	StartedAt time.Time                   `json:"startedAt,omitempty"`
}

func GetKVSandboxDataPrefix(clientID string) string {
	return "client/" + clientID + "/sandbox/"
}

func GetKVSandboxDataKey(clientID, sandboxID string) string {
	return GetKVSandboxDataPrefix(clientID) + sandboxID
}
