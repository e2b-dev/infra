package api

import (
	"bytes"
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
// The input byte slice is wiped after copying to secure memory.
// Returns ErrTokenEmpty if token is empty - use Destroy() to clear the token instead.
func (s *SecureToken) Set(token []byte) error {
	if len(token) == 0 {
		return ErrTokenEmpty
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Destroy old token first (zeros memory)
	if s.buffer != nil {
		s.buffer.Destroy()
		s.buffer = nil
	}

	// Create new LockedBuffer from bytes (source slice is wiped by memguard)
	s.buffer = memguard.NewBufferFromBytes(token)

	return nil
}

// UnmarshalJSON implements json.Unmarshaler to securely parse a JSON string
// directly into memguard, wiping the input bytes after copying.
//
// Access tokens are hex-encoded HMAC-SHA256 hashes (64 chars of [0-9a-f]),
// so they never contain JSON escape sequences.
func (s *SecureToken) UnmarshalJSON(data []byte) error {
	// JSON strings are quoted, so minimum valid is `""` (2 bytes).
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		memguard.WipeBytes(data)

		return errors.New("invalid secure token JSON string")
	}

	content := data[1 : len(data)-1]

	// Access tokens are hex strings - reject if contains backslash
	if bytes.ContainsRune(content, '\\') {
		memguard.WipeBytes(data)

		return errors.New("invalid secure token: unexpected escape sequence")
	}

	if len(content) == 0 {
		memguard.WipeBytes(data)

		return ErrTokenEmpty
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.buffer != nil {
		s.buffer.Destroy()
		s.buffer = nil
	}

	// Allocate secure buffer and copy directly into it
	s.buffer = memguard.NewBuffer(len(content))
	copy(s.buffer.Bytes(), content)

	// Wipe the input data
	memguard.WipeBytes(data)

	return nil
}

// TakeFrom transfers the token from src to this SecureToken, destroying any
// existing token. The source token is cleared after transfer.
// This avoids copying the underlying bytes.
func (s *SecureToken) TakeFrom(src *SecureToken) {
	if src == nil || s == src {
		return
	}

	src.mu.Lock()
	defer src.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Destroy current buffer
	if s.buffer != nil {
		s.buffer.Destroy()
	}

	// Transfer ownership
	s.buffer = src.buffer
	src.buffer = nil
}

// Equals checks if token matches using constant-time comparison.
// Returns false if the receiver is nil.
func (s *SecureToken) Equals(token string) bool {
	if s == nil {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.buffer == nil || !s.buffer.IsAlive() {
		return false
	}

	return s.buffer.EqualTo([]byte(token))
}

// EqualsSecure compares this token with another SecureToken using constant-time comparison.
// Returns false if either receiver or other is nil.
func (s *SecureToken) EqualsSecure(other *SecureToken) bool {
	if s == nil || other == nil {
		return false
	}

	if s == other {
		return s.IsSet()
	}

	other.mu.RLock()
	defer other.mu.RUnlock()

	if other.buffer == nil || !other.buffer.IsAlive() {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.buffer == nil || !s.buffer.IsAlive() {
		return false
	}

	return s.buffer.EqualTo(other.buffer.Bytes())
}

// IsSet returns true if a token is stored.
// Returns false if the receiver is nil.
func (s *SecureToken) IsSet() bool {
	if s == nil {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.buffer != nil && s.buffer.IsAlive()
}

// Bytes returns a copy of the token bytes (for signature generation).
// The caller should zero the returned slice after use.
// Returns ErrTokenNotSet if the receiver is nil.
func (s *SecureToken) Bytes() ([]byte, error) {
	if s == nil {
		return nil, ErrTokenNotSet
	}

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
// No-op if the receiver is nil.
func (s *SecureToken) Destroy() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.buffer != nil {
		s.buffer.Destroy()
		s.buffer = nil
	}
}
