package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

func TestServer_Update_MetadataOnly(t *testing.T) {
	tests := []struct {
		name            string
		sandboxID       string
		request         *orchestrator.SandboxUpdateRequest
		existingSandbox *sandbox.Sandbox
		wantErr         bool
		wantCode        codes.Code
		validateFunc    func(t *testing.T, sbx *sandbox.Sandbox)
	}{
		{
			name:      "successful metadata update",
			sandboxID: "test-sandbox-123",
			request: &orchestrator.SandboxUpdateRequest{
				SandboxId: "test-sandbox-123",
				Metadata: map[string]string{
					"key1": "value1",
					"key2": "value2",
				},
			},
			existingSandbox: &sandbox.Sandbox{
				APIStoredConfig: &orchestrator.SandboxConfig{
					SandboxId: "test-sandbox-123",
					Metadata: map[string]string{
						"existing": "data",
					},
				},
				Metadata: &sandbox.Metadata{
					StartedAt: time.Now(),
					EndAt:     time.Now().Add(time.Hour),
				},
			},
			wantErr: false,
			validateFunc: func(t *testing.T, sbx *sandbox.Sandbox) {
				assert.NotNil(t, sbx.APIStoredConfig)
				assert.NotNil(t, sbx.APIStoredConfig.Metadata)

				// Should replace existing
				assert.Equal(t, "", sbx.APIStoredConfig.Metadata["existing"])
				assert.Equal(t, "value1", sbx.APIStoredConfig.Metadata["key1"])
				assert.Equal(t, "value2", sbx.APIStoredConfig.Metadata["key2"])
				assert.Len(t, sbx.APIStoredConfig.Metadata, 2)
			},
		},
		{
			name:      "metadata update overwrites existing keys",
			sandboxID: "test-sandbox-456",
			request: &orchestrator.SandboxUpdateRequest{
				SandboxId: "test-sandbox-456",
				Metadata: map[string]string{
					"existing": "new-value",
					"new-key":  "new-data",
				},
			},
			existingSandbox: &sandbox.Sandbox{
				APIStoredConfig: &orchestrator.SandboxConfig{
					SandboxId: "test-sandbox-456",
					Metadata: map[string]string{
						"existing": "old-value",
						"keep":     "this-value",
					},
				},
				Metadata: &sandbox.Metadata{
					StartedAt: time.Now(),
					EndAt:     time.Now().Add(time.Hour),
				},
			},
			wantErr: false,
			validateFunc: func(t *testing.T, sbx *sandbox.Sandbox) {
				assert.NotNil(t, sbx.APIStoredConfig.Metadata)

				// Should overwrite existing key
				assert.Equal(t, "new-value", sbx.APIStoredConfig.Metadata["existing"])
				assert.Equal(t, "new-data", sbx.APIStoredConfig.Metadata["new-key"])
				assert.Len(t, sbx.APIStoredConfig.Metadata, 2)
			},
		},
		{
			name:      "metadata update on sandbox with nil metadata",
			sandboxID: "test-sandbox-789",
			request: &orchestrator.SandboxUpdateRequest{
				SandboxId: "test-sandbox-789",
				Metadata: map[string]string{
					"first": "metadata",
				},
			},
			existingSandbox: &sandbox.Sandbox{
				APIStoredConfig: &orchestrator.SandboxConfig{
					SandboxId: "test-sandbox-789",
					Metadata:  nil, // nil metadata
				},
				Metadata: &sandbox.Metadata{
					StartedAt: time.Now(),
					EndAt:     time.Now().Add(time.Hour),
				},
			},
			wantErr: false,
			validateFunc: func(t *testing.T, sbx *sandbox.Sandbox) {
				assert.NotNil(t, sbx.APIStoredConfig.Metadata)
				assert.Equal(t, "metadata", sbx.APIStoredConfig.Metadata["first"])
				assert.Len(t, sbx.APIStoredConfig.Metadata, 1)
			},
		},
		{
			name:      "empty metadata update",
			sandboxID: "test-sandbox-empty",
			request: &orchestrator.SandboxUpdateRequest{
				SandboxId: "test-sandbox-empty",
				Metadata:  map[string]string{}, // empty but not nil
			},
			existingSandbox: &sandbox.Sandbox{
				APIStoredConfig: &orchestrator.SandboxConfig{
					SandboxId: "test-sandbox-empty",
					Metadata: map[string]string{
						"existing": "data",
					},
				},
				Metadata: &sandbox.Metadata{
					StartedAt: time.Now(),
					EndAt:     time.Now().Add(time.Hour),
				},
			},
			wantErr: false,
			validateFunc: func(t *testing.T, sbx *sandbox.Sandbox) {
				// Existing metadata should remain unchanged when empty metadata is sent
				assert.NotNil(t, sbx.APIStoredConfig.Metadata)
				assert.Len(t, sbx.APIStoredConfig.Metadata, 0)
			},
		},
		{
			name:      "sandbox not found",
			sandboxID: "nonexistent-sandbox",
			request: &orchestrator.SandboxUpdateRequest{
				SandboxId: "nonexistent-sandbox",
				Metadata: map[string]string{
					"key": "value",
				},
			},
			existingSandbox: nil, // No sandbox in cache
			wantErr:         true,
			wantCode:        codes.NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create server with sandbox cache
			s := &server{
				sandboxes: smap.New[*sandbox.Sandbox](),
				tracer:    noop.NewTracerProvider().Tracer("test"),
				info:      &service.ServiceInfo{ClientId: "test-client"},
			}

			// Add existing sandbox to cache if provided
			if tt.existingSandbox != nil {
				s.sandboxes.Insert(tt.sandboxID, tt.existingSandbox)
			}

			// Call Update method
			ctx := context.Background()
			_, err := s.Update(ctx, tt.request)

			// Check error expectations
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != codes.OK {
					statusErr, ok := status.FromError(err)
					assert.True(t, ok)
					assert.Equal(t, tt.wantCode, statusErr.Code())
				}
				return
			}

			assert.NoError(t, err)

			// Validate the sandbox state after update
			if tt.validateFunc != nil {
				updatedSandbox, exists := s.sandboxes.Get(tt.sandboxID)
				assert.True(t, exists)
				tt.validateFunc(t, updatedSandbox)
			}
		})
	}
}

func TestServer_Update_EndTimeAndMetadata(t *testing.T) {
	// Test updating both end time and metadata together
	s := &server{
		sandboxes: smap.New[*sandbox.Sandbox](),
		tracer:    noop.NewTracerProvider().Tracer("test"),
		info:      &service.ServiceInfo{ClientId: "test-client"},
	}

	originalEndTime := time.Now().Add(time.Hour)
	newEndTime := time.Now().Add(2 * time.Hour)

	existingSandbox := &sandbox.Sandbox{
		APIStoredConfig: &orchestrator.SandboxConfig{
			SandboxId: "combo-test-sandbox",
			Metadata: map[string]string{
				"original": "value",
			},
		},
		Metadata: &sandbox.Metadata{
			StartedAt: time.Now(),
			EndAt:     originalEndTime,
		},
	}

	s.sandboxes.Insert("combo-test-sandbox", existingSandbox)

	request := &orchestrator.SandboxUpdateRequest{
		SandboxId: "combo-test-sandbox",
		EndTime:   timestamppb.New(newEndTime),
		Metadata: map[string]string{
			"new":      "metadata",
			"original": "updated-value",
		},
	}

	ctx := context.Background()
	_, err := s.Update(ctx, request)

	assert.NoError(t, err)

	// Verify both end time and metadata were updated
	updatedSandbox, exists := s.sandboxes.Get("combo-test-sandbox")
	assert.True(t, exists)

	// Check end time update
	assert.WithinDuration(t, newEndTime, updatedSandbox.EndAt, time.Millisecond)

	// Check metadata update
	assert.NotNil(t, updatedSandbox.APIStoredConfig.Metadata)
	assert.Equal(t, "updated-value", updatedSandbox.APIStoredConfig.Metadata["original"])
	assert.Equal(t, "metadata", updatedSandbox.APIStoredConfig.Metadata["new"])
	assert.Len(t, updatedSandbox.APIStoredConfig.Metadata, 2)
}

func TestServer_Update_OnlyEndTime(t *testing.T) {
	// Test updating only end time (existing behavior)
	s := &server{
		sandboxes: smap.New[*sandbox.Sandbox](),
		tracer:    noop.NewTracerProvider().Tracer("test"),
		info:      &service.ServiceInfo{ClientId: "test-client"},
	}

	originalEndTime := time.Now().Add(time.Hour)
	newEndTime := time.Now().Add(3 * time.Hour)

	existingSandbox := &sandbox.Sandbox{
		APIStoredConfig: &orchestrator.SandboxConfig{
			SandboxId: "timeout-test-sandbox",
			Metadata: map[string]string{
				"should": "remain-unchanged",
			},
		},
		Metadata: &sandbox.Metadata{
			StartedAt: time.Now(),
			EndAt:     originalEndTime,
		},
	}

	s.sandboxes.Insert("timeout-test-sandbox", existingSandbox)

	request := &orchestrator.SandboxUpdateRequest{
		SandboxId: "timeout-test-sandbox",
		EndTime:   timestamppb.New(newEndTime),
		// No metadata field
	}

	ctx := context.Background()
	_, err := s.Update(ctx, request)

	assert.NoError(t, err)

	// Verify only end time was updated, metadata unchanged
	updatedSandbox, exists := s.sandboxes.Get("timeout-test-sandbox")
	assert.True(t, exists)

	// Check end time update
	assert.WithinDuration(t, newEndTime, updatedSandbox.EndAt, time.Millisecond)

	// Check metadata unchanged
	assert.NotNil(t, updatedSandbox.APIStoredConfig.Metadata)
	assert.Equal(t, "remain-unchanged", updatedSandbox.APIStoredConfig.Metadata["should"])
	assert.Len(t, updatedSandbox.APIStoredConfig.Metadata, 1)
}

func TestServer_Update_NoUpdates(t *testing.T) {
	// Test request with neither end time nor metadata
	s := &server{
		sandboxes: smap.New[*sandbox.Sandbox](),
		tracer:    noop.NewTracerProvider().Tracer("test"),
		info:      &service.ServiceInfo{ClientId: "test-client"},
	}

	existingSandbox := &sandbox.Sandbox{
		APIStoredConfig: &orchestrator.SandboxConfig{
			SandboxId: "no-update-sandbox",
		},
		Metadata: &sandbox.Metadata{
			StartedAt: time.Now(),
			EndAt:     time.Now().Add(time.Hour),
		},
	}

	s.sandboxes.Insert("no-update-sandbox", existingSandbox)

	request := &orchestrator.SandboxUpdateRequest{
		SandboxId: "no-update-sandbox",
		// No EndTime, no Metadata
	}

	ctx := context.Background()
	_, err := s.Update(ctx, request)

	// Should succeed but no changes applied
	assert.NoError(t, err)

	// Verify sandbox exists and unchanged
	updatedSandbox, exists := s.sandboxes.Get("no-update-sandbox")
	assert.True(t, exists)
	assert.Equal(t, existingSandbox, updatedSandbox) // Should be identical
}
