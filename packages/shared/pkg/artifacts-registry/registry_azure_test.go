package artifacts_registry

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeACR scripts the four registry calls Delete makes, and records which
// deletes actually happened.
type fakeACR struct {
	tagDigest         string
	getTagErr         error
	deleteTagErr      error
	manifestTags      []*string
	getManifestErr    error
	deleteManifestErr error

	deletedTag      bool
	deletedManifest bool
}

func (f *fakeACR) GetTagProperties(_ context.Context, _ string, _ string, _ *azcontainerregistry.ClientGetTagPropertiesOptions) (azcontainerregistry.ClientGetTagPropertiesResponse, error) {
	if f.getTagErr != nil {
		return azcontainerregistry.ClientGetTagPropertiesResponse{}, f.getTagErr
	}

	resp := azcontainerregistry.ClientGetTagPropertiesResponse{}
	if f.tagDigest != "" {
		resp.Tag = &azcontainerregistry.TagAttributes{Digest: &f.tagDigest}
	}

	return resp, nil
}

func (f *fakeACR) DeleteTag(_ context.Context, _ string, _ string, _ *azcontainerregistry.ClientDeleteTagOptions) (azcontainerregistry.ClientDeleteTagResponse, error) {
	if f.deleteTagErr != nil {
		return azcontainerregistry.ClientDeleteTagResponse{}, f.deleteTagErr
	}
	f.deletedTag = true

	return azcontainerregistry.ClientDeleteTagResponse{}, nil
}

func (f *fakeACR) GetManifestProperties(_ context.Context, _ string, _ string, _ *azcontainerregistry.ClientGetManifestPropertiesOptions) (azcontainerregistry.ClientGetManifestPropertiesResponse, error) {
	if f.getManifestErr != nil {
		return azcontainerregistry.ClientGetManifestPropertiesResponse{}, f.getManifestErr
	}

	return azcontainerregistry.ClientGetManifestPropertiesResponse{
		ArtifactManifestProperties: azcontainerregistry.ArtifactManifestProperties{
			Manifest: &azcontainerregistry.ManifestAttributes{Tags: f.manifestTags},
		},
	}, nil
}

func (f *fakeACR) DeleteManifest(_ context.Context, _ string, _ string, _ *azcontainerregistry.ClientDeleteManifestOptions) (azcontainerregistry.ClientDeleteManifestResponse, error) {
	if f.deleteManifestErr != nil {
		return azcontainerregistry.ClientDeleteManifestResponse{}, f.deleteManifestErr
	}
	f.deletedManifest = true

	return azcontainerregistry.ClientDeleteManifestResponse{}, nil
}

func azureNotFound() error {
	return &azcore.ResponseError{StatusCode: http.StatusNotFound}
}

func TestAzureDelete(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("429 throttled")

	tests := []struct {
		name string
		fake *fakeACR

		wantErr             error
		wantErrText         string
		wantDeletedTag      bool
		wantDeletedManifest bool
	}{
		{
			name:    "missing tag maps to ErrImageNotExists",
			fake:    &fakeACR{getTagErr: azureNotFound()},
			wantErr: ErrImageNotExists,
		},
		{
			name:        "tag lookup transient error surfaces",
			fake:        &fakeACR{getTagErr: transientErr},
			wantErrText: "failed to get tag properties",
		},
		{
			name:        "tag without digest is an error",
			fake:        &fakeACR{},
			wantErrText: "did not contain a digest",
		},
		{
			name:           "untagged manifest is deleted",
			fake:           &fakeACR{tagDigest: "sha256:d1"},
			wantDeletedTag: true, wantDeletedManifest: true,
		},
		{
			name:           "shared manifest is kept when other tags remain",
			fake:           &fakeACR{tagDigest: "sha256:d1", manifestTags: []*string{new("other-build")}},
			wantDeletedTag: true, wantDeletedManifest: false,
		},
		{
			name:           "manifest already gone is success",
			fake:           &fakeACR{tagDigest: "sha256:d1", getManifestErr: azureNotFound()},
			wantDeletedTag: true, wantDeletedManifest: false,
		},
		{
			name:           "transient manifest re-read failure does not fail a completed delete",
			fake:           &fakeACR{tagDigest: "sha256:d1", getManifestErr: transientErr},
			wantDeletedTag: true, wantDeletedManifest: false,
		},
		{
			name:           "manifest delete failure is best-effort",
			fake:           &fakeACR{tagDigest: "sha256:d1", deleteManifestErr: transientErr},
			wantDeletedTag: true, wantDeletedManifest: false,
		},
		{
			name:        "tag delete failure surfaces",
			fake:        &fakeACR{tagDigest: "sha256:d1", deleteTagErr: transientErr},
			wantErrText: "failed to delete tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &AzureArtifactsRegistry{repositoryName: "repo", client: tt.fake}
			err := r.Delete(t.Context(), "template", "build-1")

			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			case tt.wantErrText != "":
				require.ErrorContains(t, err, tt.wantErrText)
			default:
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantDeletedTag, tt.fake.deletedTag, "tag deletion")
			assert.Equal(t, tt.wantDeletedManifest, tt.fake.deletedManifest,
				"manifest deletion — inverting the tags-remaining check deletes sibling builds' images")
		})
	}
}

func TestNewAzureArtifactsRegistryRejectsLoginServerAsName(t *testing.T) {
	// The env var takes the bare registry name; a login server would silently
	// become myregistry.azurecr.io.azurecr.io and fail at first use with a
	// DNS error far from the misconfiguration.
	t.Setenv("AZURE_CONTAINER_REGISTRY_NAME", "myregistry.azurecr.io")
	t.Setenv("AZURE_DOCKER_REPOSITORY_NAME", "e2b")

	_, err := NewAzureArtifactsRegistry(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bare registry name")
}
