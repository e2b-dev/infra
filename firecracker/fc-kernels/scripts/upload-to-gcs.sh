#!/usr/bin/env bash
# Uploads a fc-kernels build tree to GCS at:
#   <deployment destination>/vmlinux-<version>_<short_hash>/<arch>/<file>
#
# Reads what build.sh wrote: builds/vmlinux-<version>/<arch>/<file>. Anything
# outside an arch directory is skipped, which drops the legacy flat boot image.
# Existing objects are never overwritten, so a re-run is safe.
#
# Usage:
#   ./scripts/upload-to-gcs.sh --builds <dir> --hash <hash> --deployment <name> [--dry-run]
#
# Options:
#   --builds <dir>       Build tree to upload, holding vmlinux-<version>/ directories.
#   --hash <hash>        Commit the build came from; its short form names the object.
#   --deployment <name>  public, or a cluster name whose bucket root comes from
#                        FC_CLUSTER_BUCKET_ROOT.
#   --dry-run            Print what would be uploaded without writing.
#   -h, --help           Show this help.

set -euo pipefail

BUILDS=""
HASH=""
DEPLOYMENT=""
DRY_RUN=false

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --builds)     BUILDS="${2:?--builds needs a value}"; shift 2 ;;
    --hash)       HASH="${2:?--hash needs a value}"; shift 2 ;;
    --deployment) DEPLOYMENT="${2:?--deployment needs a value}"; shift 2 ;;
    --dry-run)    DRY_RUN=true; shift ;;
    -h|--help)    usage 0 ;;
    *) echo "Unknown argument: $1" >&2; usage 1 ;;
  esac
done

[[ -n "$BUILDS"     ]] || { echo "ERROR: --builds is required"     >&2; usage 1; }
[[ -n "$HASH"       ]] || { echo "ERROR: --hash is required"       >&2; usage 1; }
[[ -n "$DEPLOYMENT" ]] || { echo "ERROR: --deployment is required" >&2; usage 1; }
[[ -d "$BUILDS"     ]] || { echo "ERROR: no build tree at '$BUILDS'" >&2; exit 1; }

command -v gcloud >/dev/null || { echo "ERROR: gcloud CLI not found" >&2; exit 1; }

[[ "$HASH" =~ ^[0-9a-f]{7,40}$ ]] || {
  echo "ERROR: --hash '$HASH' is not a commit hash" >&2
  exit 1
}
SHORT_HASH="${HASH:0:7}"

# Cluster bucket names are not public, so their roots arrive through the
# environment, and cluster hosts read versions at the root with no prefix.
case "$DEPLOYMENT" in
  public) BUCKET_URI="gs://e2b-artifact-binaries/kernels" ;;
  *)
    [[ -n "${FC_CLUSTER_BUCKET_ROOT:-}" ]] || {
      echo "ERROR: unknown deployment '$DEPLOYMENT'" >&2
      echo "The shared destination is public. A cluster deployment needs FC_CLUSTER_BUCKET_ROOT set to its bucket root, e.g. gs://a-bucket" >&2
      exit 1
    }
    BUCKET_URI="${FC_CLUSTER_BUCKET_ROOT%/}"
    ;;
esac

echo "Source:  ${BUILDS} (commit ${SHORT_HASH})"
echo "Target:  ${BUCKET_URI}"
$DRY_RUN && echo "Mode:    dry-run"

shopt -s nullglob
FILES=("$BUILDS"/vmlinux-*/*/*)
shopt -u nullglob

[[ "${#FILES[@]}" -gt 0 ]] || {
  echo "ERROR: no vmlinux-<version>/<arch>/ files under '$BUILDS'" >&2
  exit 1
}

uploaded=0
skipped=0
for src in "${FILES[@]}"; do
  [[ -f "$src" ]] || continue
  file="$(basename "$src")"
  arch="$(basename "$(dirname "$src")")"
  version="$(basename "$(dirname "$(dirname "$src")")")"
  version="${version#vmlinux-}"

  dst="${BUCKET_URI}/vmlinux-${version}_${SHORT_HASH}/${arch}/${file}"

  if gcloud storage ls "$dst" >/dev/null 2>&1; then
    echo "  EXISTS  $dst"
    skipped=$((skipped + 1))
    continue
  fi

  if $DRY_RUN; then
    echo "  WOULD   $src -> $dst"
    continue
  fi

  echo "  UPLOAD  $src -> $dst"
  gcloud storage cp "$src" "$dst"
  uploaded=$((uploaded + 1))
done

echo ""
if $DRY_RUN; then
  echo "Dry run complete. Already in GCS: $skipped."
else
  echo "Done. Uploaded: $uploaded, already in GCS: $skipped."
fi
