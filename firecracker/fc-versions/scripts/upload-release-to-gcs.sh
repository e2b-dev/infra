#!/usr/bin/env bash
# Uploads firecracker-{amd64,arm64} assets from a fc-versions GitHub release
# to GCS at:
#   <deployment destination>/<version_name>/<arch>/firecracker
#
# Existing objects are never overwritten.
#
# Usage:
#   ./scripts/upload-release-to-gcs.sh --tag <tag> --deployment <name> [--dry-run] [--repo <repo>]
#
# Options:
#   --tag <tag>          Release tag / version name (e.g. v1.14-0.1.0).
#   --deployment <name>  public, or a cluster name whose bucket root comes from
#                        FC_CLUSTER_BUCKET_ROOT.
#   --repo <repo>        GitHub repo (default: e2b-dev/fc-versions).
#   --dry-run            Print what would be uploaded without writing.
#   -h, --help           Show this help.

set -euo pipefail

REPO="e2b-dev/fc-versions"
TAG=""
DEPLOYMENT=""
DRY_RUN=false

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)        TAG="${2:?--tag needs a value}"; shift 2 ;;
    --deployment) DEPLOYMENT="${2:?--deployment needs a value}"; shift 2 ;;
    --repo)       REPO="${2:?--repo needs a value}"; shift 2 ;;
    --dry-run)    DRY_RUN=true; shift ;;
    -h|--help)    usage 0 ;;
    *) echo "Unknown argument: $1" >&2; usage 1 ;;
  esac
done

[[ -n "$TAG"        ]] || { echo "ERROR: --tag is required"        >&2; usage 1; }
[[ -n "$DEPLOYMENT" ]] || { echo "ERROR: --deployment is required" >&2; usage 1; }

command -v gh     >/dev/null || { echo "ERROR: gh CLI not found"     >&2; exit 1; }
command -v gcloud >/dev/null || { echo "ERROR: gcloud CLI not found" >&2; exit 1; }

# Cluster buckets are not public names, so their roots arrive through the
# environment.
case "$DEPLOYMENT" in
  public) BUCKET_URI="gs://e2b-artifact-binaries/firecrackers" ;;
  *)
    [[ -n "${FC_CLUSTER_BUCKET_ROOT:-}" ]] || {
      echo "ERROR: unknown deployment '$DEPLOYMENT'" >&2
      echo "The shared destination is public. A cluster deployment needs FC_CLUSTER_BUCKET_ROOT set to its bucket root, e.g. gs://a-bucket" >&2
      exit 1
    }
    BUCKET_URI="${FC_CLUSTER_BUCKET_ROOT%/}"
    ;;
esac

if ! gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
  echo "ERROR: release '$TAG' not found in $REPO" >&2
  exit 1
fi

echo "Release: $TAG"
echo "Target:  ${BUCKET_URI}"
$DRY_RUN && echo "Mode:    dry-run"

ASSETS=()
while IFS= read -r line || [[ -n "$line" ]]; do
  [[ -n "$line" ]] && ASSETS+=("$line")
done < <(gh release view "$TAG" --repo "$REPO" --json assets \
  --jq '.assets[].name')

if [[ "${#ASSETS[@]}" -eq 0 ]]; then
  echo "ERROR: release $TAG has no assets" >&2
  exit 1
fi

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

uploaded=0
skipped=0
for asset in "${ASSETS[@]}"; do
  if [[ "$asset" =~ ^firecracker-(amd64|arm64)$ ]]; then
    arch="${BASH_REMATCH[1]}"
    dst="${BUCKET_URI}/${TAG}/${arch}/firecracker"
  elif [[ "$asset" =~ ^firecracker-debug-(amd64|arm64)\.debug$ ]]; then
    # Debug-symbols companion (DWARF) for the debug FC binary. Debug-only.
    arch="${BASH_REMATCH[1]}"
    dst="${BUCKET_URI}/${TAG}/${arch}/firecracker-debug.debug"
  elif [[ "$asset" =~ ^firecracker-debug-(amd64|arm64)$ ]]; then
    # gdb-enabled debug FC binary. Never the prod path ("firecracker"); fetched
    # explicitly by the dev-node debugging workflow.
    arch="${BASH_REMATCH[1]}"
    dst="${BUCKET_URI}/${TAG}/${arch}/firecracker-debug"
  else
    continue
  fi

  if gcloud storage ls "$dst" >/dev/null 2>&1; then
    echo "  EXISTS  $dst"
    skipped=$((skipped + 1))
    continue
  fi

  if $DRY_RUN; then
    echo "  WOULD   $asset -> $dst"
    uploaded=$((uploaded + 1))
    continue
  fi

  echo "  UPLOAD  $asset -> $dst"
  gh release download "$TAG" --repo "$REPO" \
    --pattern "$asset" --dir "$TMP_DIR" --clobber >/dev/null
  gcloud storage cp "$TMP_DIR/$asset" "$dst"
  rm -f "$TMP_DIR/$asset"
  uploaded=$((uploaded + 1))
done

echo ""
if $DRY_RUN; then
  echo "Dry run complete. Would upload: $uploaded, already in GCS: $skipped."
else
  echo "Done. Uploaded: $uploaded, already in GCS: $skipped."
fi
