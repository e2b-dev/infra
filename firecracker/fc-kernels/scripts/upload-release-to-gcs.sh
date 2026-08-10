#!/usr/bin/env bash
# Uploads vmlinux-*-{amd64,arm64}.bin assets from a fc-kernels GitHub release
# to GCS at:
#   gs://<bucket>/vmlinux-<version>_<short_hash>/<arch>/vmlinux.bin
#
# Existing objects are never overwritten. The legacy non-arch release asset
# (vmlinux-<version>.bin) is intentionally skipped — under a fresh
# hash-suffixed version name there is no pre-existing flat layout to be
# backwards-compatible with.
#
# Usage:
#   ./scripts/upload-release-to-gcs.sh --hash <hash> --bucket <bucket> [--dry-run] [--repo <repo>]
#
# Options:
#   --hash <hash>      Commit hash (full or short prefix) of the build to upload.
#   --bucket <bucket>  Target bucket (with optional path prefix), e.g.
#                        my-bucket
#                        my-bucket/kernels
#                        gs://my-bucket/kernels
#   --repo <repo>      GitHub repo (default: e2b-dev/fc-kernels).
#   --dry-run          Print what would be uploaded without writing.
#   -h, --help         Show this help.

set -euo pipefail

REPO="e2b-dev/fc-kernels"
HASH=""
BUCKET=""
DRY_RUN=false

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --hash)    HASH="${2:?--hash needs a value}"; shift 2 ;;
    --bucket)  BUCKET="${2:?--bucket needs a value}"; shift 2 ;;
    --repo)    REPO="${2:?--repo needs a value}"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    -h|--help) usage 0 ;;
    *) echo "Unknown argument: $1" >&2; usage 1 ;;
  esac
done

[[ -n "$HASH"   ]] || { echo "ERROR: --hash is required"   >&2; usage 1; }
[[ -n "$BUCKET" ]] || { echo "ERROR: --bucket is required" >&2; usage 1; }

command -v gh     >/dev/null || { echo "ERROR: gh CLI not found"     >&2; exit 1; }
command -v gcloud >/dev/null || { echo "ERROR: gcloud CLI not found" >&2; exit 1; }

BUCKET="${BUCKET#gs://}"
BUCKET="${BUCKET%/}"
BUCKET_URI="gs://${BUCKET}"

if ! FULL_HASH=$(gh api "repos/$REPO/commits/$HASH" --jq '.sha' 2>/dev/null) \
  || [[ -z "$FULL_HASH" || "$FULL_HASH" == "null" ]]; then
  echo "ERROR: commit '$HASH' not found in $REPO" >&2
  exit 1
fi
SHORT_HASH="${FULL_HASH:0:7}"

# The release workflow writes "Built from commit <full_sha>" into the body, so
# we locate the matching release by scanning bodies.
RELEASE_TAG=$(gh api "repos/$REPO/releases?per_page=100" --paginate \
  --jq ".[] | select((.body // \"\") | contains(\"$FULL_HASH\")) | .tag_name" \
  | head -1)

if [[ -z "$RELEASE_TAG" ]]; then
  echo "ERROR: no release in $REPO references commit $FULL_HASH" >&2
  exit 1
fi

echo "Release: $RELEASE_TAG (commit ${SHORT_HASH})"
echo "Target:  ${BUCKET_URI}"
$DRY_RUN && echo "Mode:    dry-run"

ASSETS=()
while IFS= read -r line || [[ -n "$line" ]]; do
  [[ -n "$line" ]] && ASSETS+=("$line")
done < <(gh release view "$RELEASE_TAG" --repo "$REPO" --json assets \
  --jq '.assets[] | select(.name | test("^vmlinux-.*\\.(bin|debug)$")) | .name')

if [[ "${#ASSETS[@]}" -eq 0 ]]; then
  echo "ERROR: release $RELEASE_TAG has no vmlinux-*.bin assets" >&2
  exit 1
fi

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

uploaded=0
skipped=0
for asset in "${ASSETS[@]}"; do
  if [[ "$asset" =~ ^vmlinux-(.+)-(amd64|arm64)\.bin$ ]]; then
    version="${BASH_REMATCH[1]}"
    arch="${BASH_REMATCH[2]}"
    dst="${BUCKET_URI}/vmlinux-${version}_${SHORT_HASH}/${arch}/vmlinux.bin"
  elif [[ "$asset" =~ ^vmlinux-(.+)-(amd64|arm64)\.debug$ ]]; then
    # DWARF debug companion for the boot image. Fetched only when debugging.
    version="${BASH_REMATCH[1]}"
    arch="${BASH_REMATCH[2]}"
    dst="${BUCKET_URI}/vmlinux-${version}_${SHORT_HASH}/${arch}/vmlinux.debug"
  else
    # Legacy non-arch release asset or unrecognized name — not uploaded.
    continue
  fi

  if gcloud storage ls "$dst" >/dev/null 2>&1; then
    echo "  EXISTS  $dst"
    skipped=$((skipped + 1))
    continue
  fi

  if $DRY_RUN; then
    echo "  WOULD   $asset -> $dst"
    continue
  fi

  echo "  UPLOAD  $asset -> $dst"
  gh release download "$RELEASE_TAG" --repo "$REPO" \
    --pattern "$asset" --dir "$TMP_DIR" --clobber >/dev/null
  gcloud storage cp "$TMP_DIR/$asset" "$dst"
  rm -f "$TMP_DIR/$asset"
  uploaded=$((uploaded + 1))
done

echo ""
if $DRY_RUN; then
  echo "Dry run complete. Already in GCS: $skipped."
else
  echo "Done. Uploaded: $uploaded, already in GCS: $skipped."
fi
