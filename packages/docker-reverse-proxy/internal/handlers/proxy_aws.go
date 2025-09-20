package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/utils"
)

type AWSProxyHandler struct {
	apiStore       *APIStore
	repositoryName string
	region         string
	accountID      string
}

func NewAWSProxyHandler(apiStore *APIStore) *AWSProxyHandler {
	return &AWSProxyHandler{
		apiStore:       apiStore,
		repositoryName: os.Getenv("AWS_DOCKER_REPOSITORY_NAME"),
		region:         os.Getenv("AWS_REGION"),
		accountID:      os.Getenv("AWS_ACCOUNT_ID"),
	}
}

func (a *AWSProxyHandler) ValidatePath(path string) bool {
	repoPrefix := "/v2/e2b/custom-envs/"
	realRepoPrefix := fmt.Sprintf("/v2/%s.dkr.ecr.%s.amazonaws.com/%s/", a.accountID, a.region, a.repositoryName)
	
	return strings.HasPrefix(path, repoPrefix) || strings.HasPrefix(path, realRepoPrefix)
}

func (a *AWSProxyHandler) TransformPath(originalPath string, templateID string) string {
	repoPrefix := "/v2/e2b/custom-envs/"
	realRepoPrefix := fmt.Sprintf("/v2/%s.dkr.ecr.%s.amazonaws.com/%s/", a.accountID, a.region, a.repositoryName)
	
	return strings.Replace(originalPath, repoPrefix, realRepoPrefix, 1)
}

func (a *AWSProxyHandler) GetExpectedTagFormat(templateID string, buildID string) string {
	return fmt.Sprintf("%s_%s", templateID, buildID)
}

func (a *AWSProxyHandler) HandleProxy(w http.ResponseWriter, req *http.Request, token *AuthToken) error {
	path := req.URL.String()
	
	repoPrefix := "/v2/e2b/custom-envs/"
	realRepoPrefix := fmt.Sprintf("/v2/%s.dkr.ecr.%s.amazonaws.com/%s/", a.accountID, a.region, a.repositoryName)
	
	if !a.ValidatePath(path) {
		// The request shouldn't need any other endpoints, we deny access
		log.Printf("No matching route found for path: %s\n", path)
		w.WriteHeader(http.StatusForbidden)
		return fmt.Errorf("no matching route found for path: %s", path)
	}

	templateID := token.TemplateID

	// Uploading blobs doesn't have the template ID in the path for AWS ECR
	if strings.HasPrefix(path, fmt.Sprintf("%sblobs/uploads/", realRepoPrefix)) {
		a.apiStore.ServeHTTP(w, req)
		return nil
	}

	pathInRepo := strings.TrimPrefix(path, repoPrefix)
	
	// For AWS, we expect the format: templateID:templateID_buildID
	// Extract the templateID from the path
	pathParts := strings.Split(pathInRepo, "/")
	if len(pathParts) > 0 {
		templateWithTag := strings.Split(pathParts[0], ":")
		if len(templateWithTag) > 0 {
			pathTemplateID := templateWithTag[0]
			
			// If the template ID in the path is different from the token template ID, deny access
			if pathTemplateID != templateID {
				w.WriteHeader(http.StatusForbidden)
				log.Printf("Access denied for template: %s (expected: %s)\n", pathTemplateID, templateID)
				return fmt.Errorf("access denied for template: %s", pathTemplateID)
			}
			
			// For AWS, we need to transform the tag format
			// From: templateID:templateID_buildID to repository:templateID_buildID
			if len(templateWithTag) > 1 {
				// Validate that the tag follows the expected format (templateID_buildID)
				expectedPrefix := templateID + "_"
				if !strings.HasPrefix(templateWithTag[1], expectedPrefix) {
					w.WriteHeader(http.StatusBadRequest)
					log.Printf("Invalid tag format for AWS: %s (expected format: %s_buildID)\n", templateWithTag[1], templateID)
					return fmt.Errorf("invalid tag format: %s", templateWithTag[1])
				}
			}
		}
	}

	// Transform the path for AWS ECR
	req.URL.Path = a.TransformPath(req.URL.Path, templateID)

	// Set the Authorization header for the request to the real docker registry
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.DockerToken))

	a.apiStore.ServeHTTP(w, req)
	return nil
}
