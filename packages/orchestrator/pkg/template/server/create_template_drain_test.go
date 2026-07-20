//go:build linux

package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func TestTemplateCreateRejectsWhileDraining(t *testing.T) {
	info := &service.ServiceInfo{}
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Draining))
	store := &ServerStore{info: info}

	_, err := store.TemplateCreate(t.Context(), &templatemanager.TemplateCreateRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("TemplateCreate() error = %v, want unavailable", err)
	}
}

func TestTemplateCreateRejectsWhileStandby(t *testing.T) {
	info := &service.ServiceInfo{}
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Standby))
	store := &ServerStore{info: info}

	_, err := store.TemplateCreate(t.Context(), &templatemanager.TemplateCreateRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err))
}
