package configuration

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type Config struct {
	RedisUrl       string
	RedisReaderUrl *string

	ServicePort int
	ServiceIpv4 string
}

type Adapter interface {
	GetConfiguration(ctx context.Context) (*Config, error)
}

const (
	SourceEnv             = "ENV"
	SourceAwsWithEnv      = "AWS-SECRET-MANAGER"
	SourceAwsWithMetadata = "AWS-SECRET-MANAGER-EC2-METADATA"

	SourceDefault = SourceEnv
	SourceEnvName = "CONFIGURATION_SOURCE"
)

func NewConfigurationAdapter() (Adapter, error) {
	configurationSource := env.GetEnv(SourceEnvName, SourceDefault)

	switch configurationSource {
	case SourceAwsWithMetadata:
		return NewAdapterWithInstanceMetadata() // take secret manager ARN from EC2 instance metadata
	case SourceAwsWithEnv:
		return NewAwsAdapter() // take secret manager ARN from ENV
	case SourceEnv:
		return NewEnvAdapter()
	}

	return nil, fmt.Errorf("invalid configuration source: %s", configurationSource)
}
