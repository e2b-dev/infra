package handlers

import (
	"fmt"
	"os"
	"strings"
)

// NewDockerProxyHandler creates the appropriate proxy handler based on environment configuration
func NewDockerProxyHandler(apiStore *APIStore) (DockerProxyHandler, error) {
	// Check for cloud provider environment variable
	provider := strings.ToLower(os.Getenv("CLOUD_PROVIDER"))
	
	// If not set, try to infer from other environment variables
	if provider == "" {
		if os.Getenv("GCP_PROJECT_ID") != "" {
			provider = "gcp"
		} else if os.Getenv("AWS_ACCOUNT_ID") != "" {
			provider = "aws"
		} else {
			// Default to GCP for backward compatibility
			provider = "gcp"
		}
	}
	
	switch provider {
	case "gcp":
		return NewGCPProxyHandler(apiStore), nil
	case "aws":
		// Validate required AWS environment variables
		if os.Getenv("AWS_DOCKER_REPOSITORY_NAME") == "" {
			return nil, fmt.Errorf("AWS_DOCKER_REPOSITORY_NAME environment variable is required for AWS provider")
		}
		if os.Getenv("AWS_REGION") == "" {
			return nil, fmt.Errorf("AWS_REGION environment variable is required for AWS provider")
		}
		if os.Getenv("AWS_ACCOUNT_ID") == "" {
			return nil, fmt.Errorf("AWS_ACCOUNT_ID environment variable is required for AWS provider")
		}
		return NewAWSProxyHandler(apiStore), nil
	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s (supported: gcp, aws)", provider)
	}
}
