package cfg

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

const (
	// SandboxStorageBackendMemory will use memory backend as a primary storage for sandbox data.
	// It will also keep redis populated to allow for seamless migration to redis.
	SandboxStorageBackendMemory = "memory"
	SandboxStorageBackendRedis  = "redis"
)

type Config struct {
	AdminToken string `env:"ADMIN_TOKEN"`

	AnalyticsCollectorAPIToken string `env:"ANALYTICS_COLLECTOR_API_TOKEN"`
	AnalyticsCollectorHost     string `env:"ANALYTICS_COLLECTOR_HOST"`

	ClickhouseConnectionString string `env:"CLICKHOUSE_CONNECTION_STRING"`

	LokiPassword string `env:"LOKI_PASSWORD"`
	LokiURL      string `env:"LOKI_URL,required"`
	LokiUser     string `env:"LOKI_USER"`

	NomadAddress string `env:"NOMAD_ADDRESS" envDefault:"http://localhost:4646"`
	NomadToken   string `env:"NOMAD_TOKEN"`

	PostgresConnectionString string `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	DBMaxOpenConnections     int32  `env:"DB_MAX_OPEN_CONNECTIONS"                      envDefault:"40"`
	DBMinIdleConnections     int32  `env:"DB_MIN_IDLE_CONNECTIONS"                      envDefault:"5"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`
	AuthDBMinIdleConnections          int32  `env:"AUTH_DB_MIN_IDLE_CONNECTIONS"           envDefault:"5"`
	AuthDBMaxOpenConnections          int32  `env:"AUTH_DB_MAX_OPEN_CONNECTIONS"           envDefault:"20"`

	PosthogAPIKey string `env:"POSTHOG_API_KEY"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`
	RedisPoolSize    int    `env:"REDIS_POOL_SIZE"     envDefault:"160"`

	APIGrpcPort uint16 `env:"API_GRPC_PORT" envDefault:"5009"`

	SandboxAccessTokenHashSeed string `env:"SANDBOX_ACCESS_TOKEN_HASH_SEED"`

	VolumesToken VolumesTokenConfig

	// SupabaseJWTSecrets is a list of secrets used to verify the Supabase JWT.
	// More secrets are possible in the case of JWT secret rotation where we need to accept
	// tokens signed with the old secret for some time.
	SupabaseJWTSecrets []string `env:"SUPABASE_JWT_SECRETS"`

	DefaultKernelVersion string `env:"DEFAULT_KERNEL_VERSION"`

	DefaultPersistentVolumeType string `env:"DEFAULT_PERSISTENT_VOLUME_TYPE"`

	// SandboxStorageBackend selects the sandbox storage implementation.
	// "redis" uses Redis directly; "populate_redis" uses in-memory with Redis shadow writes.
	SandboxStorageBackend string `env:"SANDBOX_STORAGE_BACKEND" envDefault:"memory"`

	DomainName string `env:"DOMAIN_NAME" envDefault:""`
}

type JWTSigningKey any

type VolumesTokenConfig struct {
	Issuer         string            `env:"VOLUME_TOKEN_ISSUER,required"`
	SigningMethod  jwt.SigningMethod `env:"VOLUME_TOKEN_SIGNING_METHOD,required"`
	SigningKey     JWTSigningKey     `env:"VOLUME_TOKEN_SIGNING_KEY,required"`
	SigningKeyName string            `env:"VOLUME_TOKEN_SIGNING_KEY_NAME,required"`
	Duration       time.Duration     `env:"VOLUME_TOKEN_DURATION"                  envDefault:"1h"`
}

var (
	ErrInvalidJWTSigningKey = errors.New("JWT signing key must be in the format '$TYPE:base64($VALUE)'")
	ErrUnknownKeyType       = errors.New("unknown JWT signing key type")

	parserFuncs = map[reflect.Type]env.ParserFunc{
		reflect.TypeFor[JWTSigningKey](): func(v string) (any, error) {
			keyPieces := strings.SplitN(v, ":", 2)
			if len(keyPieces) != 2 {
				return nil, ErrInvalidJWTSigningKey
			}

			keyType := keyPieces[0]
			keyValue, err := base64.StdEncoding.DecodeString(keyPieces[1])
			if err != nil {
				return nil, errors.New("JWT signing key must be base64 encoded")
			}

			switch strings.ToUpper(keyType) {
			case "ECDSA":
				return jwt.ParseECPrivateKeyFromPEM(keyValue)
			case "RSA":
				return jwt.ParseRSAPrivateKeyFromPEM(keyValue)
			case "HMAC":
				return keyValue, nil
			case "ED25519":
				return jwt.ParseEdPrivateKeyFromPEM(keyValue)
			default:
				return nil, fmt.Errorf("%s: %w", keyType, ErrUnknownKeyType)
			}
		},
		reflect.TypeFor[jwt.SigningMethod](): func(v string) (any, error) {
			method := jwt.GetSigningMethod(v)
			if method == nil {
				return nil, fmt.Errorf("unknown signing method: %s", v)
			}

			return method, nil
		},
	}
)

func Parse() (Config, error) {
	config, err := env.ParseAsWithOptions[Config](env.Options{
		FuncMap: parserFuncs,
	})
	if err != nil {
		return Config{}, err
	}

	if config.DefaultKernelVersion == "" {
		config.DefaultKernelVersion = featureflags.DefaultKernelVersion
	}

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	if !slices.Contains([]string{SandboxStorageBackendMemory, SandboxStorageBackendRedis}, config.SandboxStorageBackend) {
		return config, fmt.Errorf("invalid sandbox storage backend: %s", config.SandboxStorageBackend)
	}

	return config, nil
}
