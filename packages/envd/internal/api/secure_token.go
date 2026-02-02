package api

import (
	"errors"
	"sync"

	"github.com/awnumar/memguard"
)

var (
	ErrTokenNotSet = errors.New("access token not set")
	ErrTokenEmpty  = errors.New("empty token not allowed")
)

// SecureToken wraps memguard for secure token storage.
// It uses LockedBuffer which provides memory locking, guard pages,
// and secure zeroing on destroy.
type SecureToken struct {
	mu     sync.RWMutex
	buffer *memguard.LockedBuffer
}

// Set securely replaces the token, destroying the old one first.
// The old token memory is zeroed before the new token is stored.
// Returns ErrTokenEmpty if token is empty - use Destroy() to clear the token instead.
func (s *SecureToken) Set(token string) error {
	if token == "" {
		return ErrTokenEmpty
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Destroy old token first (zeros memory)
	if s.buffer != nil {
		s.buffer.Destroy()
		s.buffer = nil
	}

	// Create new LockedBuffer from bytes (source is wiped automatically)
	s.buffer = memguard.NewBufferFromBytes([]byte(token))

	return nil
}

// Equals checks if token matches using constant-time comparison.
func (s *SecureToken) Equals(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.buffer == nil || !s.buffer.IsAlive() {
		return false
	}

	return s.buffer.EqualTo([]byte(token))
}

// IsSet returns true if a token is stored.
func (s *SecureToken) IsSet() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.buffer != nil && s.buffer.IsAlive()
}

// Bytes returns a copy of the token bytes (for signature generation).
// The caller should zero the returned slice after use.
func (s *SecureToken) Bytes() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.buffer == nil || !s.buffer.IsAlive() {
		return nil, ErrTokenNotSet
	}

	// Return a copy (unavoidable for signature generation)
	src := s.buffer.Bytes()
	result := make([]byte, len(src))
	copy(result, src)

	return result, nil
}

// Destroy securely wipes the token from memory.
func (s *SecureToken) Destroy() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.buffer != nil {
		s.buffer.Destroy()
		s.buffer = nil
	}
}
