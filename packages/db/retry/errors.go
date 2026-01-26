package retry

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL error code classes that are retriable.
// See: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	// Connection errors (Class 08)
	pgErrClassConnection = "08"
	// Transaction rollback errors (Class 40)
	pgErrClassTransactionRollback = "40"
	// Operator intervention (Class 57)
	pgErrClassOperatorIntervention = "57"
)

// Specific PostgreSQL error codes that are retriable.
const (
	// Too many connections (53300)
	pgErrTooManyConnections = "53300"
)

// IsRetriable determines if an error is retriable.
// Returns true for transient errors that may succeed on retry.
func IsRetriable(err error) bool {
	if err == nil {
		return false
	}

	// Never retry context errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for PostgreSQL errors
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return isRetriablePgError(pgErr)
	}

	// Check for connection errors
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return true
	}

	// Check for network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Check for specific syscall errors
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// Check for connection closed errors by message
	errMsg := err.Error()
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "connection is closed") ||
		strings.Contains(errMsg, "closed network connection") ||
		strings.Contains(errMsg, "connection timed out") {
		return true
	}

	return false
}

// isRetriablePgError checks if a PostgreSQL error is retriable based on its code.
func isRetriablePgError(pgErr *pgconn.PgError) bool {
	code := pgErr.Code

	// Check error class (first two characters)
	if len(code) >= 2 {
		class := code[:2]
		switch class {
		case pgErrClassConnection: // Connection exceptions
			return true
		case pgErrClassTransactionRollback: // Transaction rollback
			return true
		case pgErrClassOperatorIntervention: // Operator intervention
			return true
		}
	}

	// Check specific error codes
	if code == pgErrTooManyConnections {
		return true
	}

	return false
}
