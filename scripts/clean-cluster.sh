#!/bin/bash
# Clean cluster state: templates (DB), GCS bucket, NFS cache, build cache.
# Preserves: base, network-egress-test (permanent templates).
# Usage: ./scripts/clean-cluster.sh
set -euo pipefail

BUCKET="${TEMPLATE_BUCKET_NAME:-e2b-staging-lev-fc-templates}"
KEEP="gtjfpksmxd9ct81x1f8e|70tbaz5vjj7bdrgpc8x2"  # base, network-egress-test

echo "Deleting stale templates from DB ..."
e2b template list --no-color 2>/dev/null \
  | grep -oP '(?<=\s)[a-z0-9]{20}(?=\s)' \
  | grep -vE "$KEEP" \
  | while read -r tid; do
      echo "  deleting $tid"
      e2b template delete "$tid" -y 2>/dev/null || true
    done

echo "Wiping GCS bucket gs://$BUCKET ..."
gsutil -m rm -r "gs://$BUCKET/**" 2>&1 | tail -1 || echo "(bucket already empty)"

ALLOC=$(nomad job status orchestrator-dev 2>/dev/null \
  | awk '/running/ && /client-orchestrator/ {print $1}')

if [ -z "$ALLOC" ]; then
  echo "ERROR: no running orchestrator alloc found"
  exit 1
fi
echo "Orchestrator alloc: $ALLOC"

echo "Clearing NFS chunks cache ..."
nomad alloc exec -task start "$ALLOC" /bin/rm -rf /orchestrator/shared-store/chunks-cache
nomad alloc exec -task start "$ALLOC" /bin/mkdir -p /orchestrator/shared-store/chunks-cache

echo "Clearing build cache ..."
# List and remove contents, keep the directory itself
for sub in $(nomad alloc exec -task start "$ALLOC" /bin/ls /orchestrator/build/ 2>/dev/null); do
  nomad alloc exec -task start "$ALLOC" /bin/rm -rf "/orchestrator/build/$sub"
done

echo "Done. Rebuild base with: make -C packages/shared build-base-template"
