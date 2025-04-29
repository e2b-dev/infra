package configuration

import (
	"fmt"
	"os"
)

const (
	sourceEnv             = "ENV"
	sourceAwsWithEnv      = "AWS-SECRET-MANAGER"
	sourceAwsWithMetadata = "AWS-SECRET-MANAGER-EC2-METADATA"

	sourceDefault = sourceEnv
)

var (
	configurationSource = os.Getenv("CONFIGURATION_SOURCE")
	awsSecretManagerARN = os.Getenv("CONFIGURATION_AWS_SECRET_MANAGER_ARN")
)

func NewAutoConfigurationAdapter() (Adapter, error) {
	switch configurationSource {
	case sourceAwsWithMetadata: // take secret manager ARN from EC2 instance metadata
		return newAwsAdapterWithMetadata()
	case sourceAwsWithEnv: // take secret manager ARN from ENV
		return newAwsAdapterWithEnv()
	case sourceEnv:
		return newEnvAdapter()
	default:
		return newEnvAdapter()
	}
}

func newAwsAdapterWithMetadata() (Adapter, error) {
	return NewAdapterWithInstanceMetadata()
}

func newAwsAdapterWithEnv() (Adapter, error) {
	if awsSecretManagerARN == "" {
		return nil, fmt.Errorf("CONFIGURATION_AWS_SECRET_MANAGER_ARN is not set")
	}

	return NewAwsAdapter(awsSecretManagerARN)
}

func newEnvAdapter() (Adapter, error) {
	panic("not implemented")
}
