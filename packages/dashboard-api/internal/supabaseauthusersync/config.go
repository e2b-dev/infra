package supabaseauthusersync

import "time"

const (
	defaultBatchSize    int32         = 50
	defaultPollInterval time.Duration = 2 * time.Second
	defaultLockTimeout  time.Duration = 2 * time.Minute
	defaultMaxAttempts  int32         = 20
)

type Config struct {
	Enabled      bool
	BatchSize    int32
	PollInterval time.Duration
	LockTimeout  time.Duration
	MaxAttempts  int32
}

func DefaultConfig() Config {
	return Config{
		Enabled:      false,
		BatchSize:    defaultBatchSize,
		PollInterval: defaultPollInterval,
		LockTimeout:  defaultLockTimeout,
		MaxAttempts:  defaultMaxAttempts,
	}
}
