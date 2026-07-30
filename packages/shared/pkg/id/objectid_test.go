package id

import (
	"bytes"
	"encoding/base32"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// goldenV7 is five real v7 UUIDs, minted 37ms apart, with the encodings they
// must produce. The strings were computed independently with Python's
// base64.b32encode rather than captured from Encode, so they check the
// implementation rather than record it, and they are the compatibility
// contract: change the alphabet, the width or the rotation and these stop
// matching.
//
// Note what the rotation did: the five share "agp2kg" from index 10, which
// is the common timestamp showing through, and their fronts differ.
var goldenV7 = []struct{ id, want string }{
	{"019fa519-bf79-724d-8811-a2bfda9755fa", "uk75vf2v7iagp2kgn7pfze3car"},
	{"019fa519-bf9f-762a-a916-431479cc7171", "imkhttdroeagp2kgn7t53cvkiw"},
	{"019fa519-bfc5-784d-9386-a5d7a93a692a", "uxl2sotjfiagp2kgn7yv4e3e4g"},
	{"019fa519-bfeb-79f2-aa2a-0addf5b9c0d9", "blo7looa3eagp2kgn75n47fkrk"},
	{"019fa519-c011-7c8e-a039-cb3ba8ca8c16", "zm52rsumcyagp2kgoacf6i5ibz"},
}

// value is the UUID's 128-bit value, for building inputs at exact boundaries.
func value(id uuid.UUID) *big.Int {
	b := [16]byte(id)

	return new(big.Int).SetBytes(b[:])
}

func fromValue(v *big.Int) uuid.UUID {
	var b [16]byte
	v.FillBytes(b[:])

	return uuid.UUID(b)
}

// v4 and v7 build UUIDs with the right version and variant bits from the
// test's own source, so the corpus is reproducible.
func v4(r *rand.Rand) uuid.UUID {
	var b [16]byte
	r.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return uuid.UUID(b)
}

func v7(r *rand.Rand) uuid.UUID {
	var b [16]byte
	r.Read(b[:])
	ms := uint64(1577836800000 + r.Int63n(1<<41)) // 2020-ish through 2080-ish
	b[0], b[1], b[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	b[3], b[4], b[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80

	return uuid.UUID(b)
}

// anyBytes is neither: 16 uniform bytes with no version, for the claim that
// Encode is total over 2^128 and not just over the UUIDs that exist.
func anyBytes(r *rand.Rand) uuid.UUID {
	var b [16]byte
	r.Read(b[:])

	return uuid.UUID(b)
}

// corpus is the shared input: both versions, the extremes, and every power of
// two either side, since a digit boundary falls every 5 bits and that is
// where a packing mistake would show.
func corpus(t *testing.T, seed int64, n int) []uuid.UUID {
	t.Helper()
	r := rand.New(rand.NewSource(seed))

	ids := []uuid.UUID{
		uuid.Nil,
		uuid.Max,
		uuid.MustParse("019fa41f-41cc-761e-8868-daa906581007"), // a real v7
		uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479"), // a real v4
	}
	for bit := 1; bit < 128; bit++ {
		p := new(big.Int).Lsh(big.NewInt(1), uint(bit))
		for _, delta := range []int64{-1, 0, 1} {
			ids = append(ids, fromValue(new(big.Int).Add(p, big.NewInt(delta))))
		}
	}
	for range n {
		ids = append(ids, v4(r), v7(r), anyBytes(r))
	}

	return ids
}

// refEncode is an independent implementation of the format, stated as
// arithmetic rather than as bit shuffling: the UUID's value, moved up by the
// two slack bits, written in 26 base32 digits, then rotated. Encode is
// checked against this rather than against itself, so a change in stdlib's
// packing would show up.
func refEncode(id uuid.UUID) string {
	v := new(big.Int).Lsh(value(id), slackBits)
	mask := big.NewInt(31)

	out := make([]byte, encodedLen)
	for i := encodedLen - 1; i >= 0; i-- {
		out[i] = alphabet[new(big.Int).And(v, mask).Int64()]
		v.Rsh(v, 5)
	}

	return string(out[rotation:]) + string(out[:rotation])
}

// TestConstantsAgree pins the relations between the rotation constants:
// where the timestamp reads out, where the slack digit lands, and that the
// rotation leaves the front to the UUID's random bits. A v7's first 10
// digits (its 48 timestamp bits plus 2 of the version nibble) are the
// non-random ones, so the front is random exactly because rotation is at
// least 14: raw digits 16 through 25 lead the string.
func TestConstantsAgree(t *testing.T) {
	t.Parallel()

	if timestampIndex != encodedLen-rotation {
		t.Errorf("timestampIndex %d is not where a left rotation by %d puts index 0", timestampIndex, rotation)
	}
	if slackIndex != timestampIndex-1 {
		t.Errorf("slackIndex %d is not just before the timestamp at %d", slackIndex, timestampIndex)
	}
	if rotation <= encodedLen/2 {
		t.Errorf("rotation %d does not put the timestamp left of center", rotation)
	}
	if rotation >= encodedLen {
		t.Errorf("rotation %d is a full turn or more", rotation)
	}
}

// TestWidthIsExact pins the facts that fix the width: 25 digits cannot hold
// 128 bits, 26 hold 130, and the slack follows.
func TestWidthIsExact(t *testing.T) {
	t.Parallel()

	if encodedLen*5 < 128 {
		t.Errorf("%d digits carry %d bits, want at least 128", encodedLen, encodedLen*5)
	}
	if (encodedLen-1)*5 >= 128 {
		t.Errorf("%d digits already carry 128 bits; the width is one too many", encodedLen-1)
	}
	if slackBits != encodedLen*5-128 || slackMask != 1<<slackBits-1 {
		t.Errorf("slack is %d bits, mask %d, which does not follow from the width", slackBits, slackMask)
	}
	for _, id := range []uuid.UUID{uuid.Nil, uuid.Max} {
		if s := Encode(id); len(s) != encodedLen {
			t.Errorf("Encode(%v) = %q, width %d, want %d", id, s, len(s), encodedLen)
		}
	}
	if s := Encode(uuid.Nil); s != strings.Repeat("a", encodedLen) {
		t.Errorf("Encode(uuid.Nil) = %q, want %d of %q", s, encodedLen, "a")
	}
}

// TestValuesOneThroughSeven pins the seven smallest nonzero UUIDs, which
// exercise every quirk of the format at once and fit in one character each:
// encoding shifts the value up by the two slack bits, so 1 through 7 become
// digit values 4 through 28, always a multiple of 4 and so always canonical;
// that digit is the last one of the unrotated string, which rotation moves
// to slackIndex. Seven strings differing from Encode(uuid.Nil) in exactly
// one character say where the low bits live, that the shift happened, and
// where the rotation put it. Value 8 would be the first to spill into a
// second digit.
func TestValuesOneThroughSeven(t *testing.T) {
	t.Parallel()

	for v := int64(1); v <= 7; v++ {
		id := fromValue(big.NewInt(v))
		want := strings.Repeat("a", slackIndex) +
			string(alphabet[v<<slackBits]) +
			strings.Repeat("a", encodedLen-slackIndex-1)

		if got := Encode(id); got != want {
			t.Errorf("Encode(%v) = %q, want %q", id, got, want)
		}
		got, err := Decode(want)
		if err != nil {
			t.Fatalf("Decode(%q): %v", want, err)
		}
		if got != id {
			t.Errorf("Decode(%q) = %v, want %v", want, got, id)
		}
	}
}

// TestEveryVersion mints one UUID of each version 1 through 7 and holds the
// format to its claim of being version-agnostic: same width, same round
// trip, no version anywhere in the codec. Only v3 and v5 are deterministic
// (they hash a name), so those two also get fixed strings; the others carry
// time, node or randomness and are checked by property instead.
func TestEveryVersion(t *testing.T) {
	t.Parallel()

	versions := []struct {
		version uuid.Version
		mint    func() (uuid.UUID, error)
		want    string // "" where the version is not deterministic
	}{
		{1, uuid.NewUUID, ""},
		{2, func() (uuid.UUID, error) { return uuid.NewDCESecurity(uuid.Person, 1234) }, ""},
		{
			3, func() (uuid.UUID, error) { return uuid.NewMD5(uuid.NameSpaceDNS, []byte("e2b.dev")), nil },
			"zqr3jv64e4unusw5en5uzstl2t",
		}, // computed with python: uuid3(NAMESPACE_DNS, "e2b.dev")
		{4, uuid.NewRandom, ""},
		{
			5, func() (uuid.UUID, error) { return uuid.NewSHA1(uuid.NameSpaceDNS, []byte("e2b.dev")), nil },
			"sbn4uedphy7pqtg6w2ybj5rac4",
		}, // computed with python: uuid5(NAMESPACE_DNS, "e2b.dev")
		{6, uuid.NewV6, ""},
		{7, uuid.NewV7, ""},
	}

	for _, tc := range versions {
		id, err := tc.mint()
		if err != nil {
			t.Fatalf("v%d: %v", tc.version, err)
		}
		if id.Version() != tc.version {
			t.Fatalf("minted %v, which is v%d, want v%d", id, id.Version(), tc.version)
		}

		s := Encode(id)
		if len(s) != encodedLen {
			t.Errorf("v%d: Encode(%v) = %q, width %d, want %d", tc.version, id, s, len(s), encodedLen)
		}
		if tc.want != "" && s != tc.want {
			t.Errorf("v%d: Encode(%v) = %q, want %q", tc.version, id, s, tc.want)
		}
		back, err := Decode(s)
		if err != nil {
			t.Fatalf("v%d: Decode(%q): %v", tc.version, s, err)
		}
		if back != id {
			t.Errorf("v%d: round trip: got %v, want %v", tc.version, back, id)
		}
	}
}

// TestUnrotateInvertsRotate is the property Decode depends on, in both
// compositions, since the two amounts are no longer the same.
func TestUnrotateInvertsRotate(t *testing.T) {
	t.Parallel()

	for _, id := range corpus(t, 2, 200) {
		s := Encode(id)
		if got := unrotate(rotate(s)); got != s {
			t.Fatalf("unrotate(rotate(%q)) = %q", s, got)
		}
		if got := rotate(unrotate(s)); got != s {
			t.Fatalf("rotate(unrotate(%q)) = %q", s, got)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	ids := corpus(t, 1, 10000)
	for range 500 {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if id, err = uuid.NewRandom(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	for _, id := range ids {
		s := Encode(id)
		if len(s) != encodedLen {
			t.Fatalf("Encode(%v) = %q, width %d, want %d", id, s, len(s), encodedLen)
		}
		if s != strings.ToLower(s) {
			t.Fatalf("Encode(%v) = %q is not all lowercase", id, s)
		}
		got, err := Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		if got != id {
			t.Fatalf("round trip: got %v, want %v", got, id)
		}
	}
}

// TestEncodingIsTheBits is the claim of the format: the string is the UUID's
// bits in 5-bit groups, rotated. Checked against refEncode, which computes it
// a different way.
func TestEncodingIsTheBits(t *testing.T) {
	t.Parallel()

	for _, id := range corpus(t, 4, 2000) {
		if got, want := Encode(id), refEncode(id); got != want {
			t.Fatalf("Encode(%v) = %q, want %q", id, got, want)
		}
	}
}

// TestMatchesPythonStdlib pins the interop contract in stdlib terms: the
// encoding is exactly base32.StdEncoding (which is base64.b32encode's
// alphabet), lowercased, unpadded, rotated. A Python consumer holding nothing
// but the snippet in the package doc can decode these IDs.
func TestMatchesPythonStdlib(t *testing.T) {
	t.Parallel()

	std := base32.StdEncoding.WithPadding(base32.NoPadding)
	for _, id := range corpus(t, 14, 1000) {
		b := [16]byte(id)
		want := rotate(strings.ToLower(std.EncodeToString(b[:])))
		if got := Encode(id); got != want {
			t.Fatalf("Encode(%v) = %q, stdlib says %q", id, got, want)
		}
	}
}

// TestPythonRoundTrip runs the actual snippet from the doc comment under
// python3, if one is on PATH, and requires it to agree in both directions.
func TestPythonRoundTrip(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}

	var in strings.Builder
	ids := corpus(t, 30, 200)
	for _, id := range ids {
		fmt.Fprintf(&in, "%s %s\n", id, Encode(id))
	}

	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import base64, sys, uuid

def encode(b: bytes) -> str:
    s = base64.b32encode(b).decode().rstrip("=").lower()
    return s[16:] + s[:16]

def decode(s: str) -> bytes:
    s = s[10:] + s[:10]
    return base64.b32decode(s.upper() + "======")

for line in sys.stdin:
    u, s = line.split()
    u = uuid.UUID(u)
    assert encode(u.bytes) == s, (u, s, encode(u.bytes))
    assert decode(s) == u.bytes, (u, s)
print("ok")
`)
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("python disagreed: %v\n%s", err, out)
	}
	t.Logf("python3 agreed on %d ids, both directions", len(ids))
}

// TestTimestampIsInside is the reason the rotation exists. Two v7 UUIDs
// minted in the same millisecond share their first 52 bits (timestamp plus
// version nibble), which is their first 10 digits; rotation moves those to
// indices 10 through 19. So the insides must match and, over enough samples,
// the fronts must not.
func TestTimestampIsInside(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(7))

	sameFront := 0
	for range 1000 {
		a, b := v7(r), v7(r)
		copy(b[:6], a[:6]) // same millisecond
		b[6] = a[6]        // same version nibble, and bits 50-51 with it

		sa, sb := Encode(a), Encode(b)
		if sa[timestampIndex:timestampIndex+10] != sb[timestampIndex:timestampIndex+10] {
			t.Fatalf("same-millisecond v7s do not share the timestamp digits: %q vs %q", sa, sb)
		}
		if sa[:4] == sb[:4] {
			sameFront++
		}
	}
	// The first 4 characters are 20 random bits; collisions are ~1e-6.
	if sameFront > 2 {
		t.Errorf("%d of 1000 same-millisecond pairs share a 4-char prefix; the front is not random", sameFront)
	}
}

// TestNoCommonPrefix is the same point stated as the user sees it: a batch of
// v7s minted together used to share a long prefix, and now must not.
func TestNoCommonPrefix(t *testing.T) {
	t.Parallel()

	first := map[byte]bool{}
	for range 200 {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		first[Encode(id)[0]] = true
	}
	// 200 draws over 32 first characters: seeing fewer than 10 distinct
	// would be wildly improbable for uniform bits.
	if len(first) < 10 {
		t.Errorf("200 fresh v7s begin with only %d distinct characters; the timestamp is still leading", len(first))
	}
}

// TestFourSpellingsExist is the cost of 128 not dividing by 5. Every UUID has
// exactly four strings that stdlib decodes to it, differing only in the slack
// digit, which rotation has moved to slackIndex; exactly one is canonical and
// Decode has to reject the other three itself, because encoding/base32 will
// not.
func TestFourSpellingsExist(t *testing.T) {
	t.Parallel()

	for _, id := range corpus(t, 12, 500) {
		s := Encode(id)
		last := values[s[slackIndex]]
		if last&slackMask != 0 {
			t.Fatalf("Encode(%v) = %q, whose slack digit %d already sets slack bits", id, s, last)
		}

		accepted := 0
		for d := 0; d <= slackMask; d++ {
			alt := s[:slackIndex] + string(alphabet[last|int8(d)]) + s[slackIndex+1:]

			// stdlib decodes all four to the same UUID and reports nothing.
			raw, err := enc.DecodeString(unrotate(alt))
			if err != nil {
				t.Fatalf("stdlib rejected %q: %v", alt, err)
			}
			if uuid.UUID(raw) != id {
				t.Fatalf("stdlib decoded %q to %v, want %v", alt, uuid.UUID(raw), id)
			}

			got, err := Decode(alt)
			if d == 0 {
				if err != nil || got != id {
					t.Fatalf("Decode(%q) = %v, %v; want %v", alt, got, err, id)
				}
				accepted++

				continue
			}
			if !errors.Is(err, ErrNotCanonical) {
				t.Fatalf("Decode(%q) = %v, %v; want ErrNotCanonical", alt, got, err)
			}
		}
		if accepted != 1 {
			t.Fatalf("%v: %d of %d spellings accepted, want 1", id, accepted, slackMask+1)
		}
	}
}

// TestSlackDigitIsRestricted is the same fact seen from the outside: only 8
// of the 32 characters can ever appear at slackIndex, and every one of them
// does.
func TestSlackDigitIsRestricted(t *testing.T) {
	t.Parallel()

	want := map[byte]bool{}
	for v := 0; v < 32; v += slackMask + 1 {
		want[alphabet[v]] = true
	}

	seen := map[byte]bool{}
	for _, id := range corpus(t, 13, 5000) {
		c := Encode(id)[slackIndex]
		if !want[c] {
			t.Fatalf("Encode(%v) has %q at index %d, whose value is not a multiple of %d", id, c, slackIndex, slackMask+1)
		}
		seen[c] = true
	}
	if len(seen) != len(want) {
		t.Errorf("saw %d distinct slack digits, want all %d", len(seen), len(want))
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	t.Parallel()

	valid := Encode(uuid.MustParse("019fa41f-41cc-761e-8868-daa906581007"))

	for _, tc := range []struct {
		name string
		s    string
	}{
		{"empty", ""},
		{"too short", valid[:encodedLen-1]},
		{"too long", valid + "a"},
		{"padded", valid[:encodedLen-1] + "="},
		// Encode never emits uppercase, so accepting it would give every
		// UUID millions of spellings. Python needs .upper() before
		// b32decode for the same reason in reverse.
		{"uppercase", strings.ToUpper(valid)},
		{"one uppercase letter", func() string {
			i := strings.IndexFunc(valid, func(r rune) bool { return r >= 'a' && r <= 'z' })

			return valid[:i] + strings.ToUpper(valid[i:i+1]) + valid[i+1:]
		}()},
		{"'0' is not in the alphabet", "0" + valid[1:]},
		{"'1' is not in the alphabet", "1" + valid[1:]},
		{"'8' is not in the alphabet", "8" + valid[1:]},
		{"'9' is not in the alphabet", "9" + valid[1:]},
		{"'-' is not a digit", "-" + valid[1:]},
		{"non-ascii", valid[:encodedLen-2] + "é"},
	} {
		if got, err := Decode(tc.s); err == nil {
			t.Errorf("%s: Decode(%q) = %v, should have failed", tc.name, tc.s, got)
		}
	}

	if _, err := Decode(valid[:encodedLen-1]); !errors.Is(err, ErrBadLength) {
		t.Errorf("short input: got %v, want ErrBadLength", err)
	}

	// Both extremes decode, and all zeros is uuid.Nil rather than an error.
	for _, tc := range []struct {
		s    string
		want uuid.UUID
	}{
		{strings.Repeat("a", encodedLen), uuid.Nil},
		{Encode(uuid.Max), uuid.Max},
	} {
		got, err := Decode(tc.s)
		if err != nil {
			t.Fatalf("Decode(%q): %v", tc.s, err)
		}
		if got != tc.want {
			t.Fatalf("Decode(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestGoldenV7 checks the fixed vectors: the encodings were computed with
// Python's base64.b32encode, so this is a check on the implementation and a
// guard against any future change to the format.
func TestGoldenV7(t *testing.T) {
	t.Parallel()

	if len(goldenV7) == 0 {
		t.Fatal("no golden vectors")
	}
	for _, tc := range goldenV7 {
		id := uuid.MustParse(tc.id)
		if id.Version() != 7 {
			t.Fatalf("%s is v%d, not v7", tc.id, id.Version())
		}

		if got := Encode(id); got != tc.want {
			t.Errorf("Encode(%s) = %q, want %q", tc.id, got, tc.want)
		}
		back, err := Decode(tc.want)
		if err != nil {
			t.Fatalf("Decode(%q): %v", tc.want, err)
		}
		if back != id {
			t.Errorf("Decode(%q) = %v, want %v", tc.want, back, id)
		}
	}

	// The five goldens were minted 37ms apart, so their coarser timestamp
	// digits agree: they must share 6 characters from timestampIndex where
	// their fronts share nothing. (Not 7: the fifth was minted across a
	// boundary of the 7th digit, which advances every 2^13 ms.)
	mid := goldenV7[0].want[timestampIndex : timestampIndex+6]
	for _, tc := range goldenV7[1:] {
		if tc.want[timestampIndex:timestampIndex+6] != mid {
			t.Errorf("%q does not carry %q at index %d", tc.want, mid, timestampIndex)
		}
	}
}

// TestOrderIsGone states the trade plainly, so no one builds an index on
// these strings expecting v7's chronology to survive: rotation leads with
// random bits, so encoded order and UUID order are unrelated.
func TestOrderIsGone(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(15))

	ids := make([]uuid.UUID, 0, 5000)
	for range 5000 {
		ids = append(ids, v7(r))
	}
	// v7s sorted by value are sorted by time; count how often the encoded
	// strings agree with that order.
	inversions := 0
	for i := 1; i < len(ids); i++ {
		a, b := ids[i-1], ids[i]
		if bytes.Compare(a[:], b[:]) > 0 {
			a, b = b, a
		}
		if Encode(a) > Encode(b) {
			inversions++
		}
	}
	// Random fronts mean ~half of all pairs invert.
	if inversions < 1000 {
		t.Errorf("only %d of %d pairs invert; the encoding still leaks order", inversions, len(ids)-1)
	}
	t.Logf("%d of %d adjacent pairs sort backwards, as designed", inversions, len(ids)-1)
}
