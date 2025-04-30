package configuration

import (
	"context"
)

type Config struct {
	RedisUrl       string
	RedisReaderUrl string

	ServicePort int
	ServiceIpv4 string

	SelfUpdateSourceUrl    *string
	SelfUpdateAutoInterval int64 // in seconds
	SelfUpdateAutoEnabled  bool

	ApiUrl    string
	ApiSecret string
}

type Adapter interface {
	GetConfiguration(ctx context.Context) (*Config, error)
}
