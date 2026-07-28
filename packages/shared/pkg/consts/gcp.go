package consts

import (
	"os"
)

var (
	GCPProject             = os.Getenv("GCP_PROJECT_ID")
	Domain                 = os.Getenv("DOMAIN_NAME")
	DockerRegistry         = os.Getenv("GCP_DOCKER_REPOSITORY_NAME")
	GCPServiceAccountEmail = os.Getenv("GCP_SERVICE_ACCOUNT_EMAIL")
	GCPRegion              = os.Getenv("GCP_REGION")
)
