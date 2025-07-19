package handlers

import (
	"net/http"
)

// AuthToken represents the authentication token information
type AuthToken struct {
	TemplateID   string
	DockerToken  string
	ExpiresIn    int
}

// DockerProxyHandler defines the interface for handling docker registry proxy requests
type DockerProxyHandler interface {
	// HandleProxy processes the docker registry request
	HandleProxy(w http.ResponseWriter, req *http.Request, token *AuthToken) error
	
	// ValidatePath checks if the request path is valid for this provider
	ValidatePath(path string) bool
	
	// TransformPath converts the incoming path to the target registry path
	TransformPath(originalPath string, templateID string) string
	
	// GetExpectedTagFormat returns the expected tag format for this provider
	GetExpectedTagFormat(templateID string, buildID string) string
}
