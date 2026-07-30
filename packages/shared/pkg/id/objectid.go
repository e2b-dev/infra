// Object IDs: UUIDs encoded as 26-character lowercase base32 strings,
// rotated so a v7's timestamp starts at index 10 rather than at the front.
//
// # Format
//
// The UUID's 16 bytes are base32-encoded with the RFC 4648 section 6 alphabet
// ("A-Z2-7"), lowercased, unpadded: 26 characters. The string is then rotated
// left by 16, so what was the first character is now the 11th.
//
// Rotation is the whole trick. A v7 UUID leads with a 48-bit big-endian
// millisecond timestamp, so unrotated encodings of IDs minted together share a
// long common prefix and the leading characters are nearly constant for
// months. Rotating moves those characters inward: the string now leads with
// 10 characters of random bits, the timestamp reads out from index 10, and
// its 9.6 digits are followed by the remaining random bits.
//
// Decode undoes it by rotating left by the other 10. The two amounts differ,
// so unlike a half-length rotation this one is not its own inverse; rotate
// and unrotate are separate functions and the tests hold them together.
//
// # Python
//
// The alphabet is the one base64.b32encode uses, so the standard library on
// the other side needs no tables, only the rotation:
//
//	import base64
//
//	def encode(b: bytes) -> str:
//	    s = base64.b32encode(b).decode().rstrip("=").lower()
//	    return s[16:] + s[:16]
//
//	def decode(s: str) -> bytes:
//	    s = s[10:] + s[:10]
//	    return base64.b32decode(s.upper() + "======")
//
// # Canonical form
//
// 26 base32 digits carry 130 bits and a UUID has 128, so the final digit of
// the unrotated string holds 2 bits that are always zero. Both Go's
// encoding/base32 and Python's b32decode discard those bits without looking,
// so every UUID has four spellings that decode to it. Decode accepts only the
// one Encode produces and returns ErrNotCanonical for the other three. After
// rotation that digit sits at index 9, not at the end.
//
// Decode also accepts only lowercase, since that is all Encode emits;
// anything else in the string is one more spelling of the same UUID and is
// rejected for the same reason.

package id

import (
	"encoding/base32"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	// alphabet is RFC 4648 section 6, lowercased: what Python's
	// base64.b32encode produces once .lower() is applied. Index is digit
	// value.
	alphabet = "abcdefghijklmnopqrstuvwxyz234567"

	// encodedLen is ceil(128 / 5): the digits needed for 16 bytes. Every
	// UUID is exactly this wide; there is no padding to add or strip.
	encodedLen = 26

	// rotation is how far Encode rotates the string left, chosen so a v7's
	// timestamp starts at timestampIndex: far enough in that the front is
	// random, left of center so most of the timestamp sits in the first
	// half. Decode rotates left by the remainder.
	rotation = 16

	// timestampIndex is where the unrotated string's first character lands:
	// byte 0 of the UUID, and so bit 0 of a v7's timestamp, reads out here.
	timestampIndex = encodedLen - rotation

	// slackBits is 26*5 - 128: the always-zero bits in the final unrotated
	// digit. slackMask selects them out of that digit's value.
	slackBits = encodedLen*5 - 128
	slackMask = 1<<slackBits - 1

	// slackIndex is where that digit lands after rotation: the character
	// Decode must check for canonical form is inside the string, just
	// before the timestamp, not at the end.
	slackIndex = timestampIndex - 1
)

var (
	// ErrBadLength means the string is not the width the format fixes:
	// encodedLen for a bare body, ObjectIDLen for a prefixed object ID.
	ErrBadLength = errors.New("id: wrong length")

	// ErrNotCanonical means the string decodes, but is not the spelling
	// Encode produces: its slack digit carries bits no UUID can set.
	ErrNotCanonical = errors.New("id: not the canonical spelling")
)

// enc is stdlib base32 over the lowercase alphabet. NoPadding because the
// width is fixed: 16 bytes is 26 digits with nothing left over.
var enc = base32.NewEncoding(alphabet).WithPadding(base32.NoPadding)

// values maps a character to its digit value, -1 if it is not in the
// alphabet. Decode uses it to check canonical form before decoding.
var values = func() (v [256]int8) {
	for i := range v {
		v[i] = -1
	}
	for i := range len(alphabet) {
		v[alphabet[i]] = int8(i)
	}

	return
}()

// rotate rotates a 26-character string left by rotation; unrotate rotates
// left by the remainder, which undoes it. Composing the two in either order
// is a full turn.
func rotate(s string) string {
	return s[rotation:] + s[:rotation]
}

func unrotate(s string) string {
	return s[encodedLen-rotation:] + s[:encodedLen-rotation]
}

// Encode writes id as 26 lowercase base32 characters, rotated so the leading
// bytes of the UUID read out from the middle of the string. It cannot fail:
// any 16 bytes encode, v4 and v7 alike.
func Encode(id uuid.UUID) string {
	b := [16]byte(id)

	return rotate(enc.EncodeToString(b[:]))
}

// Decode is the exact inverse of Encode. It accepts only what Encode
// produces: 26 characters, lowercase RFC 4648 base32, rotated, with the two
// slack bits zero.
func Decode(s string) (uuid.UUID, error) {
	if len(s) != encodedLen {
		return uuid.Nil, fmt.Errorf("%w: got %d bytes, want %d", ErrBadLength, len(s), encodedLen)
	}

	// The slack digit is checked before decoding because stdlib will not
	// check it at all: it drops the low slackBits of the final digit
	// unread, so all four spellings of a UUID decode identically and the
	// difference is only visible here. The digit is the last one of the
	// unrotated string, which rotation has moved to slackIndex.
	if v := values[s[slackIndex]]; v >= 0 && v&slackMask != 0 {
		return uuid.Nil, fmt.Errorf("%w: %q at index %d sets %d bits no uuid reaches",
			ErrNotCanonical, s[slackIndex], slackIndex, slackBits)
	}

	// Membership (including case: uppercase is simply not in the alphabet)
	// is stdlib's to report.
	b, err := enc.DecodeString(unrotate(s))
	if err != nil {
		return uuid.Nil, fmt.Errorf("id: %q is not lowercase base32: %w", s, err)
	}
	if len(b) != 16 {
		return uuid.Nil, fmt.Errorf("%w: %q decoded to %d bytes, want 16", ErrBadLength, s, len(b))
	}

	return uuid.UUID(b), nil
}
