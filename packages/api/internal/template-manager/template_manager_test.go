package template_manager

import (
	"context"
	"strings"
	"testing"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

type testStatusClient interface {
	TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest, opts ...grpc.CallOption) (*template_manager.TemplateBuildStatusResponse, error)
}

type testTemplateManagerClient interface {
	SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error
	SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error
}

type fakeStatusClient struct {
	templateBuildStatusResponse *template_manager.TemplateBuildStatusResponse
	err                         error
}

func (f *fakeStatusClient) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest, opts ...grpc.CallOption) (*template_manager.TemplateBuildStatusResponse, error) {
	return f.templateBuildStatusResponse, f.err
}

type fakeTemplateManagerClient struct {
	setStatusError   error
	setFinishedError error
}

func (f fakeTemplateManagerClient) SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error {
	return f.setStatusError
}
func (f fakeTemplateManagerClient) SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error {
	return f.setFinishedError
}

func TestPollBuildStatus_getFuncToRetry(t *testing.T) {
	type fields struct {
		statusClient testStatusClient
		buildID      uuid.UUID
	}

	tests := []struct {
		name    string
		fields  fields
		wantErr bool
		status  *template_manager.TemplateBuildStatusResponse
		err     error
	}{
		{
			name: "should return error if status is nil",
			fields: fields{
				statusClient: &fakeStatusClient{
					templateBuildStatusResponse: nil,
				},
				buildID: uuid.New(),
			},
			wantErr: true,
		},
		// return context deadline exceeded
		{
			name: "should return context deadline exceeded",
			fields: fields{
				statusClient: &fakeStatusClient{
					err: errors.New("context deadline exceeded"),
				},
			},
			wantErr: true,
			status:  nil,
		},
		// if error is not context deadline exceeded, return error
		{
			name: "should return error if error is not context deadline exceeded",
			fields: fields{
				statusClient: &fakeStatusClient{
					err: errors.New("some other error"),
				},
			},
			wantErr: true,
			status:  nil,
		},
		// if status client and err are nil, return err
		{
			name: "should return error if status client and err are nil",
			fields: fields{
				statusClient: nil,
			},
			wantErr: true,
		},
		// if status client present and err is nil, return nil
		{
			name: "should return nil if status client present and err is nil",
			fields: fields{
				statusClient: &fakeStatusClient{
					err: nil,
					templateBuildStatusResponse: &template_manager.TemplateBuildStatusResponse{
						Status: template_manager.TemplateBuildState_Completed,
					},
				},
			},
			wantErr: false,
			status: &template_manager.TemplateBuildStatusResponse{
				Status: template_manager.TemplateBuildState_Completed,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &PollBuildStatus{
				statusClient: tt.fields.statusClient,
			}
			var status *template_manager.TemplateBuildStatusResponse
			retryFunc := c.getFuncToRetry(status)
			err := retryFunc()
			if tt.wantErr {
				if err == nil {
					t.Errorf("PollBuildStatus.getFuncToRetry() = %v", err)
				}
			}
			if !tt.wantErr {
				if err != nil {
					t.Errorf("PollBuildStatus.getFuncToRetry() = %v", err)
				}
			}
		})
	}
}

func TestPollBuildStatus_dispatchBasedOnStatus(t *testing.T) {
	type fields struct {
		templateManagerClient *fakeTemplateManagerClient
	}
	type args struct {
		status *template_manager.TemplateBuildStatusResponse
	}
	tests := []struct {
		name          string
		fields        fields
		args          args
		expectedError string
	}{
		{
			name: "should return error if status is nil",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: nil,
			},
			expectedError: "nil status",
		},
		{
			name: "should handle failed status",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{
					setStatusError: errors.New("failed to set status"),
				},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Failed,
				},
			},
			expectedError: "error when setting build status",
		},
		{
			name: "should handle completed status with nil metadata",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Completed,
				},
			},
			expectedError: "nil metadata",
		},
		{
			name: "should handle completed status successfully",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{
					setFinishedError: errors.New("failed to set finished"),
				},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Completed,
					Metadata: &template_manager.TemplateBuildMetadata{
						RootfsSizeKey:  100,
						EnvdVersionKey: "1.0.0",
					},
				},
			},
			expectedError: "error when finishing build",
		},
		{
			name: "should not send to done channel for building status",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Building,
				},
			},
			expectedError: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan error, 1)
			c := &PollBuildStatus{
				templateManagerClient: tt.fields.templateManagerClient,
				done:                  done,
			}

			c.dispatchBasedOnStatus(tt.args.status)

			if tt.expectedError == "" {
				select {
				case err := <-done:
					t.Errorf("Expected no error, got %v", err)
				default:
					// Success - no error sent to channel
				}
			} else {
				select {
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), tt.expectedError) {
						t.Errorf("Expected error containing %q, got %v", tt.expectedError, err)
					}
				default:
					t.Error("Expected error but none received")
				}
			}
		})
	}
}
