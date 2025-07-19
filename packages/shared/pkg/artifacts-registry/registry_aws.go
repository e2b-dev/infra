package artifacts_registry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ECRClient interface for testability
type ECRClient interface {
	DescribeRepositories(ctx context.Context, input *ecr.DescribeRepositoriesInput, opts ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	BatchDeleteImage(ctx context.Context, input *ecr.BatchDeleteImageInput, opts ...func(*ecr.Options)) (*ecr.BatchDeleteImageOutput, error)
	GetAuthorizationToken(ctx context.Context, input *ecr.GetAuthorizationTokenInput, opts ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

type AWSArtifactsRegistry struct {
	repositoryName string
	client         ECRClient
}

var (
	AwsRepositoryNameEnvVar = "AWS_DOCKER_REPOSITORY_NAME"
	AwsRepositoryName       = os.Getenv(AwsRepositoryNameEnvVar)
)

func NewAWSArtifactsRegistry(ctx context.Context) (*AWSArtifactsRegistry, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	if AwsRepositoryName == "" {
		return nil, fmt.Errorf("%s environment variable is not set", AwsRepositoryNameEnvVar)
	}

	client := ecr.NewFromConfig(cfg)

	return &AWSArtifactsRegistry{
		repositoryName: AwsRepositoryName,
		client:         client,
	}, nil
}

func (g *AWSArtifactsRegistry) Delete(ctx context.Context, templateId string, buildId string) error {
	imageIds := []types.ImageIdentifier{
		{ImageTag: &buildId},
	}

	// for AWS implementation we are using only build id as image tag
	res, err := g.client.BatchDeleteImage(ctx, &ecr.BatchDeleteImageInput{RepositoryName: &g.repositoryName, ImageIds: imageIds})
	if err != nil {
		return fmt.Errorf("failed to delete image from aws ecr: %w", err)
	}

	if len(res.Failures) > 0 {
		if res.Failures[0].FailureCode == types.ImageFailureCodeImageNotFound {
			return ErrImageNotExists
		}

		return errors.New("failed to delete image from aws ecr")
	}

	return nil
}

func (g *AWSArtifactsRegistry) GetTag(ctx context.Context, templateId string, buildId string) (string, error) {
	// Generate composite tag using templateId and buildId first (includes validation)
	compositeTag, err := GenerateCompositeTag(templateId, buildId)
	if err != nil {
		return "", fmt.Errorf("failed to generate composite tag: %w", err)
	}

	res, err := g.client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{RepositoryNames: []string{g.repositoryName}})
	if err != nil {
		return "", fmt.Errorf("failed to describe aws ecr repository: %w", err)
	}

	if len(res.Repositories) == 0 {
		return "", fmt.Errorf("repository %s not found", g.repositoryName)
	}

	return fmt.Sprintf("%s:%s", *res.Repositories[0].RepositoryUri, compositeTag), nil
}

func (g *AWSArtifactsRegistry) GetImage(ctx context.Context, templateId string, buildId string, platform containerregistry.Platform) (containerregistry.Image, error) {
	imageUrl, err := g.GetTag(ctx, templateId, buildId)
	if err != nil {
		return nil, fmt.Errorf("failed to get image URL: %w", err)
	}

	ref, err := name.ParseReference(imageUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	auth, err := g.getAuthToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *AWSArtifactsRegistry) getAuthToken(ctx context.Context) (*authn.Basic, error) {
	res, err := g.client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get aws ecr auth token: %w", err)
	}

	if len(res.AuthorizationData) == 0 {
		return nil, fmt.Errorf("no aws ecr auth token found")
	}

	authData := res.AuthorizationData[0]
	decodedToken, err := base64.StdEncoding.DecodeString(*authData.AuthorizationToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode aws ecr auth token: %w", err)
	}

	// split into username and password
	parts := strings.SplitN(string(decodedToken), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid aws ecr auth token")
	}

	username := parts[0]
	password := parts[1]

	return &authn.Basic{
		Username: username,
		Password: password,
	}, nil
}
