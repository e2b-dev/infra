package supabaseauthusersync

import "time"

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
		BatchSize:    50,
		PollInterval: 2 * time.Second,
		LockTimeout:  2 * time.Minute,
		MaxAttempts:  20,
	}
}
