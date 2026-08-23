package consts

import (
	"os"
)

// Read lazily (unlike the init-time vars in gcp.go) so tests can point the
// storage backend at a runtime-started Azurite container via t.Setenv.

func AzureStorageAccountName() string { return os.Getenv("AZURE_STORAGE_ACCOUNT_NAME") }

func AzureStorageAccountKey() string { return os.Getenv("AZURE_STORAGE_ACCOUNT_KEY") }

func AzureStorageConnectionString() string { return os.Getenv("AZURE_STORAGE_CONNECTION_STRING") }

const (
	AzureContainerRegistryNameEnvVar = "AZURE_CONTAINER_REGISTRY_NAME"
	AzureDockerRepositoryNameEnvVar  = "AZURE_DOCKER_REPOSITORY_NAME"
)

func AzureContainerRegistryName() string { return os.Getenv(AzureContainerRegistryNameEnvVar) }

func AzureDockerRepositoryName() string { return os.Getenv(AzureDockerRepositoryNameEnvVar) }
