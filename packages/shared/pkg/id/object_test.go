package id

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// nonCanonical is a 26-character body that decodes but is not what Encode
// emits: 'b' is digit value 1, and at slackIndex the low slackBits must be
// zero. Encode(uuid.Nil) is 26 'a's, so this is that string with the one
// digit that matters changed.
var nonCanonical = strings.Repeat("a", slackIndex) + "b" + strings.Repeat("a", encodedLen-slackIndex-1)

// allKinds is every kind the package defines. A kind added without being
// added here is a kind these tests do not check, so it is also listed in
// TestAllKindsAreListed.
var allKinds = []ObjectKind{KindProject, KindWorkspace}

// TestWidthIsFixed is the claim the parser rests on: every ID of every kind
// is ObjectIDLen characters, made of a prefixLen tag, the separator, and a
// body of encodedLen. If this fails the parser is indexing into strings on
// an assumption that no longer holds.
func TestWidthIsFixed(t *testing.T) {
	t.Parallel()

	if ObjectIDLen != 30 {
		t.Errorf("ObjectIDLen = %d, want 30", ObjectIDLen)
	}
	if ObjectIDLen != prefixLen+1+encodedLen {
		t.Errorf("ObjectIDLen %d is not %d + 1 + %d", ObjectIDLen, prefixLen, encodedLen)
	}
	if separatorIndex != prefixLen || bodyIndex != prefixLen+1 {
		t.Errorf("separator at %d and body at %d do not follow a prefix of %d",
			separatorIndex, bodyIndex, prefixLen)
	}

	// The extremes and a real v7, as both kinds: nothing about the width
	// depends on the value.
	for _, u := range []uuid.UUID{uuid.Nil, uuid.Max, uuid.MustParse(goldenV7[0].id)} {
		for _, s := range []string{ProjectID(u).String(), WorkspaceID(u).String()} {
			if len(s) != ObjectIDLen {
				t.Errorf("%q is %d characters, want %d", s, len(s), ObjectIDLen)
			}
			if s[separatorIndex] != prefixSeparator {
				t.Errorf("%q has %q at index %d, want %q",
					s, s[separatorIndex], separatorIndex, prefixSeparator)
			}
			if strings.Count(s, string(prefixSeparator)) != 1 {
				t.Errorf("%q has more than one %q", s, prefixSeparator)
			}
		}
	}
}

// TestAllKindsAreListed catches a kind added to the enum but not to allKinds,
// which would otherwise silently go untested. It walks past the last known
// kind rather than trusting the list.
func TestAllKindsAreListed(t *testing.T) {
	t.Parallel()

	for k := ObjectKind(1); int(k) <= len(allKinds)+8; k++ {
		defined := k.Prefix() != ""
		listed := slices.Contains(allKinds, k)
		if defined != listed {
			t.Errorf("ObjectKind(%d): prefix %q, in allKinds: %v", k, k.Prefix(), listed)
		}
	}
}

// TestPrefixesAreUsable holds the prefix table to what the format needs: each
// kind has a tag of exactly prefixLen letters, and they differ.
func TestPrefixesAreUsable(t *testing.T) {
	t.Parallel()

	seen := map[string]ObjectKind{}
	for _, k := range allKinds {
		p := k.Prefix()
		if len(p) != prefixLen {
			t.Errorf("ObjectKind(%d) has prefix %q of width %d, want %d", k, p, len(p), prefixLen)

			continue
		}
		// Letters only: no separator, no digit, no case to normalize,
		// nothing that has to be escaped in a URL or a log line.
		for i := range len(p) {
			if p[i] < 'a' || p[i] > 'z' {
				t.Errorf("prefix %q has %q at index %d, want a lowercase letter", p, p[i], i)
			}
		}
		if other, dup := seen[p]; dup {
			t.Errorf("ObjectKind(%d) and ObjectKind(%d) share the prefix %q", other, k, p)
		}
		seen[p] = k
		if k.String() != p {
			t.Errorf("ObjectKind(%d).String() = %q, want the prefix %q", k, k.String(), p)
		}
	}

	// The zero value is no kind, and says so rather than passing for one.
	var zero ObjectKind
	if zero.Prefix() != "" {
		t.Errorf("the zero ObjectKind has prefix %q, want none", zero.Prefix())
	}
	if zero.String() != "ObjectKind(0)" {
		t.Errorf("the zero ObjectKind prints as %q", zero.String())
	}
}

// TestStringIsPrefixedEncoding is the external form, against the golden UUIDs
// the codec's own tests pin: the same 26 characters, behind a tag that
// depends only on the type.
func TestStringIsPrefixedEncoding(t *testing.T) {
	t.Parallel()

	for _, g := range goldenV7 {
		u := uuid.MustParse(g.id)

		if got, want := ProjectID(u).String(), "prj_"+g.want; got != want {
			t.Errorf("ProjectID(%v).String() = %q, want %q", u, got, want)
		}
		if got, want := WorkspaceID(u).String(), "wrk_"+g.want; got != want {
			t.Errorf("WorkspaceID(%v).String() = %q, want %q", u, got, want)
		}
	}
}

func TestKindIsInTheType(t *testing.T) {
	t.Parallel()

	if got := (ProjectID{}).Kind(); got != KindProject {
		t.Errorf("ProjectID.Kind() = %v, want %v", got, KindProject)
	}
	if got := (WorkspaceID{}).Kind(); got != KindWorkspace {
		t.Errorf("WorkspaceID.Kind() = %v, want %v", got, KindWorkspace)
	}

	// Distinct types, so one cannot be assigned to the other by accident.
	// The compiler enforces this; the test records it, since a careless
	// edit could collapse both aliases onto one instantiation and nothing
	// else here would notice.
	p, w := reflect.TypeFor[ProjectID](), reflect.TypeFor[WorkspaceID]()
	if p == w {
		t.Fatalf("ProjectID and WorkspaceID are the same type %v", p)
	}
	if p.Kind() != reflect.Array || p.Size() != reflect.TypeFor[uuid.UUID]().Size() {
		t.Errorf("ProjectID is %v of size %d, want a uuid", p.Kind(), p.Size())
	}
}

// TestConvertTeamIDToProjectID: the UUID is untouched, only the name and the
// spelling change. Checked over the corpus, since a conversion that dropped
// or reordered bytes would still compile.
func TestConvertTeamIDToProjectID(t *testing.T) {
	t.Parallel()

	for _, teamID := range corpus(t, 11, 500) {
		p := ConvertTeamIDToProjectID(teamID)
		if uuid.UUID(p) != teamID {
			t.Fatalf("ConvertTeamIDToProjectID(%v) = %v, want the same uuid", teamID, uuid.UUID(p))
		}
		if p.Kind() != KindProject {
			t.Fatalf("ConvertTeamIDToProjectID(%v).Kind() = %v, want %v", teamID, p.Kind(), KindProject)
		}

		// And the round trip the other way: the string parses back to
		// the team ID it started as.
		back, err := ParseProjectID(p.String())
		if err != nil {
			t.Fatalf("ParseProjectID(%q): %v", p, err)
		}
		if uuid.UUID(back) != teamID {
			t.Fatalf("ParseProjectID(%q) = %v, want %v", p, uuid.UUID(back), teamID)
		}
	}
}

// TestRoundTripThroughStrings is the property both parsers exist for, over
// the codec's corpus: whatever String writes, Parse reads back unchanged.
func TestRoundTripThroughStrings(t *testing.T) {
	t.Parallel()

	for _, u := range corpus(t, 7, 2000) {
		p := ProjectID(u)
		gotP, err := ParseProjectID(p.String())
		if err != nil {
			t.Fatalf("ParseProjectID(%q): %v", p, err)
		}
		if gotP != p {
			t.Fatalf("ParseProjectID(%q) = %v, want %v", p, uuid.UUID(gotP), u)
		}

		w := WorkspaceID(u)
		gotW, err := ParseWorkspaceID(w.String())
		if err != nil {
			t.Fatalf("ParseWorkspaceID(%q): %v", w, err)
		}
		if gotW != w {
			t.Fatalf("ParseWorkspaceID(%q) = %v, want %v", w, uuid.UUID(gotW), u)
		}
	}
}

// TestParseRejects is the list of things that are not an ID of the kind
// asked for. Each case is run against both parsers, with the prefix
// substituted, since neither should be laxer than the other.
func TestParseRejects(t *testing.T) {
	t.Parallel()

	body := goldenV7[0].want

	cases := []struct {
		name string
		in   string // %s is the kind's own prefix, %S that prefix uppercased
		want error
	}{
		// Anything that is not ObjectIDLen characters is refused on width
		// alone, before its parts are looked at.
		{"empty", "", ErrBadLength},
		{"no separator", "%s" + body, ErrBadLength},
		{"bare body", body, ErrBadLength},
		{"prefix only", "%s", ErrBadLength},
		{"prefix and separator only", "%s_", ErrBadLength},
		{"body one short", "%s_" + body[1:], ErrBadLength},
		{"body one long", "%s_" + body + "a", ErrBadLength},
		{"two-character prefix", "pr_" + body, ErrBadLength},
		{"leading space", " %s_" + body, ErrBadLength},
		{"trailing newline", "%s_" + body + "\n", ErrBadLength},
		{"empty prefix", "_" + body, ErrBadLength},
		{"uuid instead of body", "%s_019fa519-bf79-724d-8811-a2bfda9755fa", ErrBadLength},

		// Right width, separator missing or in the wrong place. The
		// parser reads position separatorIndex and nowhere else, so a
		// separator elsewhere in the string does not stand in for it.
		{"no separator at all", "%sa" + body, ErrNoPrefix},
		{"separator at the end", "%sa" + body[:25] + "_", ErrNoPrefix},
		{"separator one early", "pr_a" + body, ErrNoPrefix},

		// Right shape, wrong tag.
		{"other kind", "zzz_" + body, ErrWrongKind},
		{"uppercase prefix", "%S_" + body, ErrWrongKind},
		{"prefix with space", " pr_" + body, ErrWrongKind},

		// Right shape and tag, body the codec refuses.
		{"non-canonical body", "%s_" + nonCanonical, ErrNotCanonical},
		{"uppercase body", "%s_" + strings.ToUpper(body), nil},
		{"digit outside alphabet", "%s_" + body[:25] + "9", nil},
		{"separator in body", "%s_" + body[:25] + "_", nil},
		{"hyphen in body", "%s_" + body[:25] + "-", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, k := range allKinds {
				in := strings.NewReplacer("%s", k.Prefix(), "%S", strings.ToUpper(k.Prefix())).Replace(tc.in)

				var err error
				switch k {
				case KindProject:
					_, err = ParseProjectID(in)
				case KindWorkspace:
					_, err = ParseWorkspaceID(in)
				}

				if err == nil {
					t.Errorf("Parse%sID(%q) = no error, want one", k, in)

					continue
				}
				if tc.want != nil && !errors.Is(err, tc.want) {
					t.Errorf("Parse%sID(%q) = %v, want %v", k, in, err, tc.want)
				}
				if !strings.Contains(err.Error(), "id: ") {
					t.Errorf("Parse%sID(%q) = %v, which does not name the package", k, in, err)
				}
			}
		})
	}
}

// TestParseRejectsTheOtherKind is the reason the prefix is there: a
// well-formed ID naming the wrong thing is refused, and the message says
// both what it got and what it wanted, so the caller can see the mixup.
func TestParseRejectsTheOtherKind(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse(goldenV7[0].id)
	project, workspace := ProjectID(u).String(), WorkspaceID(u).String()

	_, err := ParseProjectID(workspace)
	if !errors.Is(err, ErrWrongKind) {
		t.Errorf("ParseProjectID(%q) = %v, want %v", workspace, err, ErrWrongKind)
	}
	if !strings.Contains(err.Error(), `"wrk"`) || !strings.Contains(err.Error(), `"prj"`) {
		t.Errorf("ParseProjectID(%q) = %v, which does not name both kinds", workspace, err)
	}

	if _, err = ParseWorkspaceID(project); !errors.Is(err, ErrWrongKind) {
		t.Errorf("ParseWorkspaceID(%q) = %v, want %v", project, err, ErrWrongKind)
	}
}

// TestParseFailsToTheZeroValue: a rejected string yields nothing usable, so a
// caller that ignores the error gets an obviously empty ID rather than a
// partly filled one.
func TestParseFailsToTheZeroValue(t *testing.T) {
	t.Parallel()

	p, err := ParseProjectID("prj_" + nonCanonical)
	if err == nil {
		t.Fatalf("ParseProjectID(%q) = %v, want an error", "prj_"+nonCanonical, uuid.UUID(p))
	}
	if uuid.UUID(p) != uuid.Nil {
		t.Errorf("failed ParseProjectID returned %v, want the nil uuid", uuid.UUID(p))
	}
}

func TestMustParse(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse(goldenV7[0].id)

	if got := MustParseProjectID(ProjectID(u).String()); got != ProjectID(u) {
		t.Errorf("MustParseProjectID = %v, want %v", uuid.UUID(got), u)
	}
	if got := MustParseWorkspaceID(WorkspaceID(u).String()); got != WorkspaceID(u) {
		t.Errorf("MustParseWorkspaceID = %v, want %v", uuid.UUID(got), u)
	}

	for _, tc := range []struct {
		name string
		call func()
	}{
		{"project", func() { MustParseProjectID("nope") }},
		{"workspace", func() { MustParseWorkspaceID("nope") }},
		{"wrong kind", func() { MustParseProjectID(WorkspaceID(u).String()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("did not panic")
				}
			}()
			tc.call()
		})
	}
}
