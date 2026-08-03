#!/bin/bash

# Guards the *missing* direction of scripts/compression-tests.tsv.
#
# select-tests.sh already rejects stale entries (a listed test that no longer
# exists). Nothing catches the opposite rot: a new test on the snapshot
# write/read path that nobody adds to the allow-list silently never runs
# under the compressed configs. This lint closes that hole:
#
#   Every top-level test whose body touches a snapshot-lifecycle symbol
#   (the SYMBOLS list below) must either appear in the allow-list or carry
#   an explicit exclusion marker in the comment block directly above it:
#
#     // compression-tests:excluded <reason>
#
#   per the allow-list's own criterion: excluded is correct when the test's
#   *subject* is API surface / egress / proxy routing and pause/resume is
#   only a fixture; listed is correct when the subject is writing or
#   reading back a snapshot.
#
# Packages the allow-list covers with "*" are exempt (new tests there are
# always selected). Top-level helper functions that touch the symbols must
# themselves be added to SYMBOLS, so tests hiding behind package-local
# wrappers stay visible to this lint.
#
# Known limitation: symbols called through cross-package helpers
# (internal/utils) are invisible here — add the helper's name to SYMBOLS
# when such a wrapper is introduced.

set -uo pipefail

cd "$(dirname "$0")/.."

TSV="scripts/compression-tests.tsv"
TESTS_DIR="internal/tests"
MARKER="compression-tests:excluded"

# The snapshot write/read surface as seen from the tests. Extend when a new
# lifecycle call or a package-local wrapper around one is introduced.
# NB: character classes ([(]) instead of \( — backslashes do not survive
# awk -v value processing portably.
# Any autopause call counts (variable args and line-broken calls included);
# the scan strips literal WithAutoPause(false)/WithAutoResume(false) from a
# line before matching, so only the explicit opt-out stays quiet.
SYMBOLS='PostSandboxesSandboxIDPauseWithResponse|PostSandboxesSandboxIDResumeWithResponse|PostSandboxesSandboxIDForkWithResponse|PostSandboxesSandboxIDSnapshotsWithResponse|WithAutoPause[(]|WithAutoResume[(]|FsFreeze|Fsfreeze|pauseFilesystemOnly[(]|pauseSandbox[(]|createSnapshotTemplate[(]|startSnapshotInBackground[(]|createSnapshotTemplateWithCleanup[(]'

fail=0

# Packages fully covered by a "*" entry are exempt.
wildcard_pkgs=$(awk -F'\t' '!/^#/ && NF==2 && $2=="*" {print $1}' "$TSV")

is_wildcarded() {
    local pkg="$1"
    while IFS= read -r w; do
        [ "$pkg" = "$w" ] && return 0
    done <<<"$wildcard_pkgs"
    return 1
}

is_listed() {
    local pkg="$1" name="$2"
    grep -qE "^${pkg}[[:space:]]+${name}\$" "$TSV"
}

while IFS= read -r file; do
    pkg=$(dirname "$file")
    is_wildcarded "$pkg" && continue

    # Attribute symbol hits to the enclosing column-0 function; remember the
    # comment block directly above each function for the exclusion marker.
    hits=$(awk -v symre="$SYMBOLS" -v marker="$MARKER" '
        /^\/\// { cbuf = cbuf $0; next }
        /^func / {
            # Strip an optional receiver so methods attribute by name too.
            fn = $0
            sub(/^func +/, "", fn)
            sub(/^\([^)]*\) */, "", fn)
            sub(/\(.*/, "", fn)
            fline = FNR
            excluded = (cbuf ~ marker)
            cbuf = ""
        }
        # Column-0 close brace ends the function: symbols between functions
        # must not be attributed to the previous one.
        /^}/ { fn = "" }
        { if (!/^func /) cbuf = "" }
        {
            probe = $0
            gsub(/WithAutoPause\(false\)|WithAutoResume\(false\)/, "", probe)
        }
        probe ~ symre && fn != "" && !reported[fn] {
            reported[fn] = 1
            print fn "\t" fline "\t" (excluded ? "excluded" : "-")
        }
    ' "$file")

    [ -z "$hits" ] && continue

    while IFS=$'\t' read -r fn fline excluded; do
        if [[ "$fn" != Test* ]]; then
            # A package-local helper touches the snapshot surface: require it
            # in SYMBOLS so tests calling it are not invisible to this lint.
            # Exact whole-entry match ("name[(]"): a substring or prefix match
            # would let a wrapper whose name is a prefix of an existing entry
            # slip through as already listed.
            if ! tr '|' '\n' <<<"$SYMBOLS" | grep -qxF "${fn}[(]"; then
                echo "${file}:${fline}: helper ${fn} touches snapshot symbols; add '${fn}[(]' to SYMBOLS in $(basename "$0")"
                fail=1
            fi
            continue
        fi
        [ "$excluded" = "excluded" ] && continue
        if ! is_listed "$pkg" "$fn"; then
            echo "${file}:${fline}: ${fn} touches the snapshot path but is neither in ${TSV} nor marked '// ${MARKER} <reason>'"
            fail=1
        fi
    done <<<"$hits"
done < <(find "$TESTS_DIR" -name '*_test.go' | sort)

# A marker on a listed test is a contradiction — one of the two must go.
while IFS= read -r file; do
    pkg=$(dirname "$file")
    awk -v marker="$MARKER" '
        $0 ~ "^// ?" marker { m = 1; next }
        /^func Test/ && m { fn = $2; sub(/\(.*/, "", fn); print FNR "\t" fn }
        { if (!/^\/\//) m = 0 }
    ' "$file" | while IFS=$'\t' read -r fline fn; do
        if is_listed "$pkg" "$fn"; then
            echo "${file}:${fline}: ${fn} is both allow-listed and marked ${MARKER} — remove one"
            exit 9
        fi
    done
    [ $? -eq 9 ] && fail=1
done < <(grep -rl "$MARKER" "$TESTS_DIR" --include='*_test.go' 2>/dev/null)

if [ "$fail" -ne 0 ]; then
    echo ""
    echo "Snapshot-path tests must run under the compressed configs or opt out"
    echo "explicitly. Either add the test to ${TSV} (subject: writing or"
    echo "reading back a snapshot) or put '// ${MARKER} <reason>' directly"
    echo "above it (subject: API surface / egress / routing; snapshot is fixture)."
    exit 1
fi

echo "allow-list completeness: every snapshot-path test is listed or explicitly excluded"
