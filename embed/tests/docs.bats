#!/usr/bin/env bats

# docs/overview.svg is drawn light and re-coloured for dark mode by one
# @media (prefers-color-scheme: dark) block. That block matches on the literal
# hex in each attribute ([fill="#F7F8FA"] and friends), so a colour changed in
# the drawing keeps its light value in dark mode unless its selector changes
# with it — nothing renders wrong, the shape just stays bright on a dark page.
# These tests pair the two sets in both directions. No Docker needed.

setup() {
  cd "$BATS_TEST_DIRNAME/.." || return 1
}

@test "overview.svg carries one dark-scheme style block" {
  [ "$(grep -c '<style' docs/overview.svg)" -eq 1 ]
  grep -q 'prefers-color-scheme: dark' docs/overview.svg
}

@test "every colour in overview.svg has a dark-scheme override" {
  # The selectors spell the same fill="#XXXXXX" text as the attributes they
  # match, so the attribute side skips the lines that start one; otherwise a
  # selector left behind by a deleted element would vouch for itself.
  attributes="$(grep -v '^[[:space:]]*\[' docs/overview.svg |
    grep -oE '(fill|stroke)="#[0-9A-Fa-f]{6}"' | sort -u)"
  selectors="$(grep -oE '\[(fill|stroke)="#[0-9A-Fa-f]{6}"\]' docs/overview.svg |
    tr -d '[]' | sort -u)"
  [ -n "$attributes" ]
  [ -n "$selectors" ]

  while read -r value; do
    if ! printf '%s\n' "$selectors" | grep -qxF "$value"; then
      echo "$value is drawn but the dark-scheme block does not override it" >&2
      return 1
    fi
  done <<<"$attributes"

  while read -r value; do
    if ! printf '%s\n' "$attributes" | grep -qxF "$value"; then
      echo "the dark-scheme block overrides $value, which nothing carries" >&2
      return 1
    fi
  done <<<"$selectors"
}

# The hub README's port table and the overview diagram state the same eleven
# ports in different notation (one row per port; a range in the picture). The
# table is the one in README.md at the package root, the hub -- the three
# install guides point at it rather than repeating it. Expand the ranges and
# compare both ways, so a port added to one and not the other fails.
#
# The picture also names the ports of the four stores, which the table does not
# list because nothing outside the machine can reach them. One of those,
# Postgres on 5432, lands inside the numeric window scanned below, so the text
# that draws a loopback port carries data-scope="loopback" and is dropped here.
# The tag cannot hide a host-network port: the assertion under it fails if a
# port from the table ever appears on a tagged line.
@test "the ports drawn in overview.svg are the hub README table's, no more and no fewer" {
  table="$(grep -oE '^\| [0-9]{4} \|' README.md | grep -oE '[0-9]{4}' | sort -u)"
  [ -n "$table" ]
  while read -r port; do
    if grep 'data-scope="loopback"' docs/overview.svg | grep -qE "\\b$port\\b"; then
      echo "$port is in the README's table but drawn as a loopback port" >&2
      return 1
    fi
  done <<<"$table"
  en_dash="$(printf '\342\200\223')"
  drawn="$(sed "s/$en_dash/-/g" docs/overview.svg |
    grep -v 'data-scope="loopback"' |
    grep -oE '\b[0-9]{4}(-[0-9]{4})?\b' | while IFS= read -r tok; do
    case "$tok" in
      *-*) seq "${tok%-*}" "${tok#*-}" ;;
      *) echo "$tok" ;;
    esac
  done | awk '$1 >= 3000 && $1 <= 5999' | sort -u)"
  [ -n "$drawn" ]
  diff <(echo "$table") <(echo "$drawn")
}

# The guides quote FIX lines their reader will meet in a log, so what is on the
# page can be matched against what the terminal printed. A quote that drifts
# from the script sends that reader hunting for a string nothing prints, which
# is exactly what happened when the host scripts' hints were made shape-neutral
# and the compose guide kept the old wording. Nothing else compares the two.
#
# Inline code spans are unwrapped first: markdown soft-wraps a long span across
# source lines, so a span's text is its source lines joined with single spaces.
# `die` in the shell scripts prints "FIX: $2" and carries only the remedy in
# its source, so a quote counts as found either whole or with the "FIX: "
# prefix removed. compose.yaml is in the search set for the hints it holds
# itself.
@test "every FIX line a guide quotes is printed by a script" {
  local quoted checked=0 line
  quoted="$(python3 - README.md compose/README.md terraform/gcp/README.md \
    kubernetes/README.md <<'PY'
import re, sys

for path in sys.argv[1:]:
    fenced, prose, in_fence = [], [], False
    for raw in open(path).read().splitlines():
        if raw.lstrip().startswith("```"):
            in_fence = not in_fence
            prose.append("")          # a fence ends the paragraph around it
            continue
        (fenced if in_fence else prose).append(raw)
    for raw in fenced:
        if raw.strip().startswith("FIX:"):
            print(raw.strip())
    for para in re.split(r"\n\s*\n", "\n".join(prose)):
        joined = " ".join(para.split())
        for span in re.findall(r"`([^`]+)`", joined):
            # A bare `FIX:` names the line, it does not quote one.
            if span.startswith("FIX:") and span != "FIX:":
                print(span)
PY
)"
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    if ! grep -rqF "$line" compose/scripts compose/compose.yaml &&
       ! grep -rqF "${line#FIX: }" compose/scripts compose/compose.yaml; then
      echo "a guide quotes \"$line\", which no script prints" >&2
      return 1
    fi
    checked=$((checked + 1))
  done <<<"$quoted"
  # An extractor that stopped matching would pass every guide vacuously.
  [ "$checked" -gt 0 ]
}

# The hub and the three install guides cross-link each other, the diagrams,
# the manifests and the tests. A relative link that does not resolve is a dead
# end for whoever follows it, and nothing else checks them. URLs and bare
# in-page anchors are somebody else's business; every other target has to
# exist on disk, relative to the README that names it. Fenced code blocks are
# dropped first: a `](` inside one is not a link.
@test "every relative Markdown link in the four READMEs resolves" {
  local checked=0 readme dir target
  for readme in README.md compose/README.md terraform/gcp/README.md \
                kubernetes/README.md; do
    [ -f "$readme" ] || { echo "$readme is missing" >&2; return 1; }
    dir="$(dirname "$readme")"
    while IFS= read -r target; do
      target="${target%%#*}"
      [ -n "$target" ] || continue
      if [ ! -e "$dir/$target" ]; then
        echo "$readme links to $target, which does not exist" >&2
        return 1
      fi
      checked=$((checked + 1))
    done < <(awk '/^```/ { fenced = !fenced; next } !fenced' "$readme" |
      grep -oE '\]\([^)]+\)' | sed -E 's/^\]\(//; s/\)$//' |
      grep -vE '^[a-z][a-z0-9+.-]*:' || true)
  done
  # A regex that stopped matching would pass every README vacuously.
  [ "$checked" -gt 0 ]
}
