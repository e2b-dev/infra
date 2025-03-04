package chdb

import "fmt"

type ClickHouseConfig struct {
	ConnectionString string
	Username         string
	Password         string
	Database         string
	Debug            bool
}

func (c ClickHouseConfig) Validate() error {
	if c.ConnectionString == "" {
		return fmt.Errorf("clickhouse connection string cannot be empty string")
	}

	if c.Username == "" {
		return fmt.Errorf("clickhouse username cannot be empty string")
	}

	if c.Database == "" {
		return fmt.Errorf("clickhouse database cannot be empty string")
	}

	return nil
}
