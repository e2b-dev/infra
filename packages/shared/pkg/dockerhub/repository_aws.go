package dockerhub

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type AWSRemoteRepository struct {
	repositoryURL string
	client        *ecr.Client
}

func NewAWSRemoteRepository(ctx context.Context, repositoryURL string) (*AWSRemoteRepository, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := ecr.NewFromConfig(cfg)

	return &AWSRemoteRepository{
		repositoryURL: repositoryURL,
		client:        client,
	}, nil
}

func (g *AWSRemoteRepository) GetImage(ctx context.Context, tag string, platform containerregistry.Platform) (containerregistry.Image, error) {
	tagWithoutRegistry, err := removeRegistryFromTag(tag)
	if err != nil {
		return nil, fmt.Errorf("error removing registry from tag: %w", err)
	}

	ref, err := name.ParseReference(g.repositoryURL + "/" + tagWithoutRegistry)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	authToken, err := g.getAuthToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting auth token: %w", err)
	}

	img, err := remote.Image(ref, remote.WithAuth(authToken), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	return img, nil
}

func (g *AWSRemoteRepository) getAuthToken(ctx context.Context) (authn.Authenticator, error) {
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
