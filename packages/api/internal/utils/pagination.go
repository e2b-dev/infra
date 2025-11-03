package utils

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// generateCursor generates a cursor token from a timestamp and ID.
// The cursor format is base64-encoded "{RFC3339Nano_timestamp}__{id}".
func generateCursor(timestamp time.Time, id string) string {
	cursor := fmt.Sprintf("%s__%s", timestamp.Format(time.RFC3339Nano), id)

	return base64.URLEncoding.EncodeToString([]byte(cursor))
}

// PaginationParams holds pagination parameters from the API request
type PaginationParams struct {
	Limit     *int32
	NextToken *string
}

// PaginationConfig holds configuration for pagination behavior
type PaginationConfig struct {
	DefaultLimit int32
	MaxLimit     int32
	DefaultID    string // Default cursor ID when no token is provided (e.g., max UUID or max sandbox ID)
}

// Cursor represents a parsed pagination cursor
type Cursor struct {
	Time time.Time
	ID   string
}

// Pagination handles pagination logic for list endpoints
type Pagination[T any] struct {
	config    PaginationConfig
	limit     int32
	cursor    Cursor
	nextToken *string
}

// NewPagination creates a new pagination instance from request parameters
func NewPagination[T any](params PaginationParams, config PaginationConfig) (*Pagination[T], error) {
	p := &Pagination[T]{
		config: config,
	}

	// Parse and validate limit
	p.limit = config.DefaultLimit
	if params.Limit != nil {
		p.limit = *params.Limit
	}
	if p.limit > config.MaxLimit {
		p.limit = config.MaxLimit
	}

	// Parse cursor token
	var err error
	p.cursor, err = parseCursorToken(params.NextToken, config.DefaultID)
	if err != nil {
		return nil, fmt.Errorf("invalid next token: %w", err)
	}

	return p, nil
}

// QueryLimit returns the limit to use for database queries (limit + 1 to detect more results)
func (p *Pagination[T]) QueryLimit() int32 {
	return p.limit + 1
}

// CursorTime returns the cursor timestamp
func (p *Pagination[T]) CursorTime() time.Time {
	return p.cursor.Time
}

// CursorID returns the cursor ID
func (p *Pagination[T]) CursorID() string {
	return p.cursor.ID
}

// setNextToken sets the next token from the last item in the results
func (p *Pagination[T]) setNextToken(timestamp time.Time, id string) {
	cursor := generateCursor(timestamp, id)
	p.nextToken = &cursor
}

// hasMore checks if there are more results based on the result count
func (p *Pagination[T]) hasMore(resultCount int) bool {
	return resultCount > int(p.limit)
}

// trimResults trims the results to the requested limit if there are more
func (p *Pagination[T]) trimResults(results []T) []T {
	if p.hasMore(len(results)) {
		return results[:p.limit]
	}

	return results
}

// processResults handles pagination: checks for more results, sets next token from last item, and trims results.
// The getTimestampAndID function extracts the timestamp and ID from each result item.
func (p *Pagination[T]) processResults(results []T, getTimestampAndID func(T) (time.Time, string)) []T {
	if p.hasMore(len(results)) {
		lastItem := results[p.limit-1]
		timestamp, id := getTimestampAndID(lastItem)
		p.setNextToken(timestamp, id)
	}

	return p.trimResults(results)
}

// ProcessResultsWithHeader handles pagination and sets the X-Next-Token header in one call.
// This is a convenience method that combines ProcessResults and SetHeader.
func (p *Pagination[T]) ProcessResultsWithHeader(c *gin.Context, results []T, getTimestampAndID func(T) (time.Time, string)) []T {
	trimmed := p.processResults(results, getTimestampAndID)
	p.setHeader(c)

	return trimmed
}

// setHeader sets the X-Next-Token header if there are more results
func (p *Pagination[T]) setHeader(c *gin.Context) {
	if p.nextToken != nil {
		c.Header("X-Next-Token", *p.nextToken)
	}
}

// parseCursorToken parses a cursor token, returning default values if token is nil/empty
func parseCursorToken(token *string, defaultID string) (Cursor, error) {
	if token != nil && *token != "" {
		cursorTime, cursorID, err := ParseCursor(*token)
		if err != nil {
			return Cursor{}, err
		}

		return Cursor{Time: cursorTime, ID: cursorID}, nil
	}

	// Default to current time and provided default ID to get the first page
	return Cursor{Time: time.Now(), ID: defaultID}, nil
}
