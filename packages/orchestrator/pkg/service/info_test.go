//go:build linux

package service

import (
	"context"
	"runtime"
	"testing"
	"time"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestDrainWaitsForAdmittedSandboxCreates(t *testing.T) {
	info := &ServiceInfo{}
	info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Healthy)

	release, admitted := info.BeginSandboxCreate()
	if !admitted {
		t.Fatal("healthy service rejected sandbox create")
	}

	drained := make(chan struct{})
	go func() {
		info.SetStatus(context.Background(), orchestratorinfo.ServiceInfoStatus_Draining)
		close(drained)
	}()

	deadline := time.Now().Add(time.Second)
	for info.statusMu.TryRLock() {
		info.statusMu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("drain never started waiting for admitted creates")
		}
		runtime.Gosched()
	}

	select {
	case <-drained:
		t.Fatal("drain completed while an admitted create was still running")
	default:
	}

	release()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("drain did not complete after the admitted create finished")
	}

	if _, admitted := info.BeginSandboxCreate(); admitted {
		t.Fatal("draining service admitted a sandbox create")
	}
}
