package client

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sync"

	_ "github.com/lib/pq" //nolint:blank-imports
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const coreTrackingTable = "_migrations"

var (
	migrationTablePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	gooseTableMu          sync.Mutex
)

func init() {
	goose.SetTableName(coreTrackingTable)
}

func CheckMigrationVersion(ctx context.Context, connectionString string, expectedMigration int64) error {
	return CheckMigrationVersionWithTable(ctx, connectionString, coreTrackingTable, expectedMigration)
}

func CheckMigrationVersionWithTable(ctx context.Context, connectionString, table string, expectedMigration int64) error {
	if !migrationTablePattern.MatchString(table) {
		return fmt.Errorf("invalid migration table name: %s", table)
	}

	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() {
		dbErr := db.Close()
		if dbErr != nil {
			logger.L().Error(ctx, "Failed to close database connection", zap.Error(dbErr))
		}
	}()

	version, err := getDBVersionWithTable(db, table)
	if err != nil {
		return fmt.Errorf("failed to get database version from %s: %w", table, err)
	}

	// Check if the database version is less than the expected migration
	// We allow higher versions to account for future migrations and rollbacks
	if version < expectedMigration {
		return fmt.Errorf("database version %d in %s is less than expected %d", version, table, expectedMigration)
	}

	logger.L().Info(ctx, "Database version",
		zap.String("table", table),
		zap.Int64("version", version),
		zap.Int64("expected_migration", expectedMigration),
	)

	return nil
}

func getDBVersionWithTable(db *sql.DB, table string) (int64, error) {
	gooseTableMu.Lock()
	defer gooseTableMu.Unlock()

	goose.SetTableName(table)
	defer goose.SetTableName(coreTrackingTable)

	return goose.GetDBVersion(db)
}
