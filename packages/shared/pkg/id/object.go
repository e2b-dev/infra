// Typed object IDs: a kind tag, an underscore, and the object's UUID in the
// encoding objectid.go defines.
//
//	prj_uk75vf2v7iagp2kgn7pfze3car
//	wrk_imkhttdroeagp2kgn7t53cvkiw
//	sec_uk75vf2v7iagp2kgn7pfze3car
//
// The tag is there for the reader and for the parser: it says what the ID
// names, so a project ID pasted where a workspace ID belongs is rejected at
// the edge with a message that says which is which, rather than becoming a
// lookup that finds nothing.
//
// # Width
//
// Every ID is exactly ObjectIDLen characters: prefixLen for the tag, one for
// the separator, encodedLen for the body. Nothing about it varies, so the
// parser reads by position rather than by searching for the separator, and a
// storage column can be a fixed 30. Holding the width means holding the
// prefixes to prefixLen, which the compile-time assertions below do: a
// two- or four-character prefix does not build.
//
// In Go the tag is carried in the type, not in the value. ObjectID is generic
// over a phantom type that names one ObjectKind. ProjectID, WorkspaceID, and
// SecretID are that generic instantiated: distinct types, so assigning one to
// another does not compile, sharing one copy of the parsing and formatting.
// The underlying type is uuid.UUID, so conversion in either direction is a
// conversion and needs no accessor:
//
//	pid := ProjectID(uuid.Must(uuid.NewV7()))
//	u := uuid.UUID(pid)
//
// Conversion between two ObjectIDs compiles too, since their underlying types
// are identical. That is deliberate: crossing kinds should be possible, but
// only by writing it down.

package id

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

// ObjectKind enumerates the kinds of object an ID can name. The zero value is
// no kind; every valid kind has a prefix.
type ObjectKind uint8

const (
	KindProject ObjectKind = iota + 1
	KindWorkspace
	KindSecret
)

// The prefixes. Three lowercase letters each, one per kind, none of them a
// prefix of another since they are all the same width.
const (
	projectPrefix   = "prj"
	workspacePrefix = "wrk"
	secretPrefix    = "sec"
)

const (
	// prefixLen is how many characters a kind tag takes. Fixed, so the
	// whole ID is fixed.
	prefixLen = 3

	// prefixSeparator divides the tag from the body. It is not in the
	// body's alphabet ("a-z2-7") and not in any prefix, so the one at
	// separatorIndex is the only one an ID can contain.
	prefixSeparator = '_'

	// separatorIndex is where that character must be, and bodyIndex where
	// the encoded UUID starts.
	separatorIndex = prefixLen
	bodyIndex      = separatorIndex + 1

	// ObjectIDLen is the width of every object ID: 3 + 1 + 26 = 30. It
	// does not vary by kind, by UUID version, or over time.
	ObjectIDLen = bodyIndex + encodedLen
)

// The width above holds only if every prefix is exactly prefixLen
// characters. These are the check, made by the compiler rather than by a
// test: an array of one length does not satisfy a declaration of another, so
// a prefix of the wrong width fails to build.
var (
	_ [prefixLen]byte = [len(projectPrefix)]byte{}
	_ [prefixLen]byte = [len(workspacePrefix)]byte{}
	_ [prefixLen]byte = [len(secretPrefix)]byte{}
)

var (
	// ErrNoPrefix means the string carries no kind tag at all: there is no
	// separator where one must be.
	ErrNoPrefix = errors.New("id: missing kind prefix")

	// ErrWrongKind means the string is a well-formed ID of another kind.
	ErrWrongKind = errors.New("id: wrong kind")
)

// Prefix returns the tag that leads an ID of this kind, or "" if k is not a
// kind this package defines.
func (k ObjectKind) Prefix() string {
	switch k {
	case KindProject:
		return projectPrefix
	case KindWorkspace:
		return workspacePrefix
	case KindSecret:
		return secretPrefix
	default:
		return ""
	}
}

// String is the prefix, which is the name these objects go by outside Go.
func (k ObjectKind) String() string {
	if p := k.Prefix(); p != "" {
		return p
	}

	return "ObjectKind(" + strconv.Itoa(int(k)) + ")"
}

// kind constrains ObjectID's type parameter. Its method is unexported, so the
// set of kinds is closed to this package: no other package can instantiate
// ObjectID with a type of its own.
type kind interface{ objectKind() ObjectKind }

type (
	projectKind   struct{}
	workspaceKind struct{}
	secretKind    struct{}
)

func (projectKind) objectKind() ObjectKind   { return KindProject }
func (workspaceKind) objectKind() ObjectKind { return KindWorkspace }
func (secretKind) objectKind() ObjectKind    { return KindSecret }

// ObjectID is a UUID that knows, in its type, what it names. Use the aliases
// below rather than naming it directly.
type ObjectID[K kind] uuid.UUID

type (
	ProjectID   = ObjectID[projectKind]
	WorkspaceID = ObjectID[workspaceKind]
	SecretID    = ObjectID[secretKind]
)

// Kind is the kind this ID names, the same for every value of the type.
func (o ObjectID[K]) Kind() ObjectKind {
	var k K

	return k.objectKind()
}

// String is the external form: prefix, underscore, 26 base32 characters,
// ObjectIDLen in all. It is what the kind-specific parsers accept, and the only
// spelling they accept.
func (o ObjectID[K]) String() string {
	return o.Kind().Prefix() + string(prefixSeparator) + Encode(uuid.UUID(o))
}

// ParseProjectID reads a project ID. It rejects IDs of any other kind, and
// anything whose body is not exactly what Encode produces.
func ParseProjectID(s string) (ProjectID, error) {
	return parseObjectID[projectKind](s)
}

// ParseWorkspaceID reads a workspace ID, under the same rules.
func ParseWorkspaceID(s string) (WorkspaceID, error) {
	return parseObjectID[workspaceKind](s)
}

// ParseSecretID reads a secret ID, under the same rules.
func ParseSecretID(s string) (SecretID, error) {
	return parseObjectID[secretKind](s)
}

// MustParseProjectID is ParseProjectID for IDs fixed in the source, where a
// failure is a bug in the program rather than in its input. It panics.
func MustParseProjectID(s string) ProjectID {
	return mustParseObjectID(ParseProjectID(s))
}

// MustParseWorkspaceID is MustParseProjectID for workspaces.
func MustParseWorkspaceID(s string) WorkspaceID {
	return mustParseObjectID(ParseWorkspaceID(s))
}

// MustParseSecretID is ParseSecretID for IDs fixed in the source. It panics on
// invalid input.
func MustParseSecretID(s string) SecretID {
	return mustParseObjectID(ParseSecretID(s))
}

// ConvertTeamIDToProjectID names a team by the word the outside world uses.
// A project is a row in public.teams: the same identity under two names, one
// internal and older than the other. The UUID is carried across unchanged;
// what changes is only how it is written and what it is called.
//
// It is a conversion, so it cannot fail, and it is spelled out as a function
// rather than left to ProjectID(teamID) so that the places where the rename
// is crossed can be found by searching for it.
func ConvertTeamIDToProjectID(teamID uuid.UUID) ProjectID {
	return ProjectID(teamID)
}

func parseObjectID[K kind](s string) (ObjectID[K], error) {
	var zero ObjectID[K]
	want := zero.Kind()

	// Width first, so the two slices below are always in range and so the
	// commonest kind of bad input is rejected by one comparison.
	if len(s) != ObjectIDLen {
		return zero, fmt.Errorf("%w: %q is %d characters, want %d (%q, %q, %d more)",
			ErrBadLength, s, len(s), ObjectIDLen, want, prefixSeparator, encodedLen)
	}
	if s[separatorIndex] != prefixSeparator {
		return zero, fmt.Errorf("%w: %q has %q at index %d, want %q",
			ErrNoPrefix, s, s[separatorIndex], separatorIndex, prefixSeparator)
	}
	if prefix := s[:prefixLen]; prefix != want.Prefix() {
		return zero, fmt.Errorf("%w: %q is a %q id, want %q", ErrWrongKind, s, prefix, want)
	}

	// The body's own errors (ErrNotCanonical, base32 membership) are
	// wrapped rather than restated: they are more precise than anything
	// this layer knows. Its length check cannot fire, the width above
	// having settled it.
	u, err := Decode(s[bodyIndex:])
	if err != nil {
		return zero, fmt.Errorf("%q is not a %s id: %w", s, want, err)
	}

	return ObjectID[K](u), nil
}

func mustParseObjectID[K kind](o ObjectID[K], err error) ObjectID[K] {
	if err != nil {
		panic(err)
	}

	return o
}
