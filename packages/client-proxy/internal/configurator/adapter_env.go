package configuration

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type EnvAdapter struct{}

const (
	RedisUrlEnv        = "REDIS_URL"
	RedisClusterUrlEnv = "REDIS_CLUSTER_URL"

	NodeIpEnv   = "NODE_IP"
	NodePortEnv = "NODE_PORT"
)

func NewEnvAdapter() (*EnvAdapter, error) {
	return &EnvAdapter{}, nil
}

func (e *EnvAdapter) GetConfiguration(_ context.Context) (*Config, error) {
	redisUrl := utils.RequiredEnv(RedisUrlEnv, "Redis URL is required")

	selfIp := utils.RequiredEnv(NodeIpEnv, "Node IP env is required")
	selfPortRaw := utils.RequiredEnv(NodePortEnv, "Node Port env is required")
	selfPort, err := strconv.Atoi(selfPortRaw)
	if err != nil {
		return nil, errors.New("node Port env is not a valid integer")
	}

	var redisReaderUrl *string = nil
	redisReaderUrlRaw := os.Getenv(RedisClusterUrlEnv)
	if redisReaderUrlRaw != "" {
		redisReaderUrl = &redisReaderUrlRaw
	} else {
		redisReaderUrl = &redisUrl
	}

	return &Config{
		RedisUrl:       redisUrl,
		RedisReaderUrl: redisReaderUrl,

		ServicePort: selfPort,
		ServiceIpv4: selfIp,
	}, nil
}
