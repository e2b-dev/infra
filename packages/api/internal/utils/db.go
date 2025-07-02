package utils

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
)

const trackingTable = "_migrations"

func CheckMigrationVersion(expectedMigration int64) error {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() {
		dbErr := db.Close()
		if dbErr != nil {
			zap.L().Error("Failed to close database connection", zap.Error(dbErr))
		}
	}()

	goose.SetTableName(trackingTable)

	version, err := goose.GetDBVersion(db)
	if err != nil {
		return fmt.Errorf("failed to get database version: %w", err)
	}

	// Check if the database version is less than the expected migration
	// We allow higher versions to account for future migrations and rollbacks
	if version < expectedMigration {
		return fmt.Errorf("database version %d is less than expected %d", version, expectedMigration)
	}

	zap.L().Info("Database version", zap.Int64("version", version), zap.Int64("expected_migration", expectedMigration))
	return nil
}
