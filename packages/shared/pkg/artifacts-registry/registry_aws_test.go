package artifacts_registry

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockECRClient is a mock implementation of ECRClient for testing
type MockECRClient struct {
	mock.Mock
}

func (m *MockECRClient) DescribeRepositories(ctx context.Context, input *ecr.DescribeRepositoriesInput, opts ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error) {
	args := m.Called(ctx, input)
	return args.Get(0).(*ecr.DescribeRepositoriesOutput), args.Error(1)
}

func (m *MockECRClient) BatchDeleteImage(ctx context.Context, input *ecr.BatchDeleteImageInput, opts ...func(*ecr.Options)) (*ecr.BatchDeleteImageOutput, error) {
	args := m.Called(ctx, input)
	return args.Get(0).(*ecr.BatchDeleteImageOutput), args.Error(1)
}

func (m *MockECRClient) GetAuthorizationToken(ctx context.Context, input *ecr.GetAuthorizationTokenInput, opts ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
	args := m.Called(ctx, input)
	return args.Get(0).(*ecr.GetAuthorizationTokenOutput), args.Error(1)
}

func TestAWSArtifactsRegistry_GetTag(t *testing.T) {
	tests := []struct {
		name           string
		templateId     string
		buildId        string
		repositoryUri  string
		mockSetup      func(*MockECRClient)
		expectedTag    string
		expectError    bool
		errorContains  string
	}{
		{
			name:          "successful composite tag generation",
			templateId:    "my-template",
			buildId:       "build-123",
			repositoryUri: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo",
			mockSetup: func(m *MockECRClient) {
				repositoryUri := "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
				m.On("DescribeRepositories", mock.Anything, mock.MatchedBy(func(input *ecr.DescribeRepositoriesInput) bool {
					return len(input.RepositoryNames) == 1 && input.RepositoryNames[0] == "test-repo"
				})).Return(&ecr.DescribeRepositoriesOutput{
					Repositories: []types.Repository{
						{
							RepositoryUri: &repositoryUri,
						},
					},
				}, nil)
			},
			expectedTag: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo:my-template_build-123",
		},
		{
			name:       "invalid template id",
			templateId: "template@invalid",
			buildId:    "build-123",
			mockSetup: func(m *MockECRClient) {
				// No mock setup needed as validation should fail before ECR call
			},
			expectError:   true,
			errorContains: "failed to generate composite tag",
		},
		{
			name:       "invalid build id",
			templateId: "my-template",
			buildId:    "build@invalid",
			mockSetup: func(m *MockECRClient) {
				// No mock setup needed as validation should fail before ECR call
			},
			expectError:   true,
			errorContains: "failed to generate composite tag",
		},
		{
			name:       "ECR describe repositories error",
			templateId: "my-template",
			buildId:    "build-123",
			mockSetup: func(m *MockECRClient) {
				m.On("DescribeRepositories", mock.Anything, mock.Anything).Return(
					(*ecr.DescribeRepositoriesOutput)(nil),
					errors.New("ECR error"),
				)
			},
			expectError:   true,
			errorContains: "failed to describe aws ecr repository",
		},
		{
			name:       "repository not found",
			templateId: "my-template",
			buildId:    "build-123",
			mockSetup: func(m *MockECRClient) {
				m.On("DescribeRepositories", mock.Anything, mock.Anything).Return(&ecr.DescribeRepositoriesOutput{
					Repositories: []types.Repository{},
				}, nil)
			},
			expectError:   true,
			errorContains: "repository test-repo not found",
		},
		{
			name:       "template id with underscores",
			templateId: "my_template_123",
			buildId:    "build_456",
			repositoryUri: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo",
			mockSetup: func(m *MockECRClient) {
				repositoryUri := "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
				m.On("DescribeRepositories", mock.Anything, mock.Anything).Return(&ecr.DescribeRepositoriesOutput{
					Repositories: []types.Repository{
						{
							RepositoryUri: &repositoryUri,
						},
					},
				}, nil)
			},
			expectedTag: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo:my_template_123_build_456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECRClient)
			tt.mockSetup(mockClient)

			registry := &AWSArtifactsRegistry{
				repositoryName: "test-repo",
				client:         mockClient,
			}

			result, err := registry.GetTag(context.Background(), tt.templateId, tt.buildId)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedTag, result)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestAWSArtifactsRegistry_GetTag_EdgeCases(t *testing.T) {
	t.Run("empty template id", func(t *testing.T) {
		mockClient := new(MockECRClient)
		registry := &AWSArtifactsRegistry{
			repositoryName: "test-repo",
			client:         mockClient,
		}

		_, err := registry.GetTag(context.Background(), "", "build-123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "template id cannot be empty")
	})

	t.Run("empty build id", func(t *testing.T) {
		mockClient := new(MockECRClient)
		registry := &AWSArtifactsRegistry{
			repositoryName: "test-repo",
			client:         mockClient,
		}

		_, err := registry.GetTag(context.Background(), "template-123", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "build id cannot be empty")
	})

	t.Run("very long template and build ids", func(t *testing.T) {
		mockClient := new(MockECRClient)
		registry := &AWSArtifactsRegistry{
			repositoryName: "test-repo",
			client:         mockClient,
		}

		// Create IDs that would result in a tag longer than 128 characters
		longTemplateId := "very-long-template-name-that-exceeds-normal-length-limits-and-should-cause-issues"
		longBuildId := "very-long-build-id-that-also-exceeds-normal-length-limits-and-should-cause-issues"

		_, err := registry.GetTag(context.Background(), longTemplateId, longBuildId)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tag length")
	})
}

func TestAWSArtifactsRegistry_GetTag_Integration(t *testing.T) {
	// This test verifies the integration between GetTag and the composite tag generation
	t.Run("composite tag format validation", func(t *testing.T) {
		mockClient := new(MockECRClient)
		repositoryUri := "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
		
		mockClient.On("DescribeRepositories", mock.Anything, mock.Anything).Return(&ecr.DescribeRepositoriesOutput{
			Repositories: []types.Repository{
				{
					RepositoryUri: &repositoryUri,
				},
			},
		}, nil)

		registry := &AWSArtifactsRegistry{
			repositoryName: "test-repo",
			client:         mockClient,
		}

		result, err := registry.GetTag(context.Background(), "template-123", "build-456")
		
		assert.NoError(t, err)
		assert.Equal(t, "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo:template-123_build-456", result)
		
		// Verify that the tag part can be parsed back
		tagPart := "template-123_build-456"
		templateId, buildId, parseErr := ParseCompositeTag(tagPart)
		assert.NoError(t, parseErr)
		assert.Equal(t, "template-123", templateId)
		assert.Equal(t, "build-456", buildId)
		
		mockClient.AssertExpectations(t)
	})
}