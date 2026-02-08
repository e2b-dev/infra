#!/bin/bash
# Delete all templates except the base template
# Base template ID: gtjfpksmxd9ct81x1f8e

set -e

BASE_TEMPLATE_ID="gtjfpksmxd9ct81x1f8e"

echo "Fetching template list..."
TEMPLATES=$(e2b template list -f json 2>/dev/null | jq -r '.[].templateID' | grep -v "$BASE_TEMPLATE_ID" || true)

if [ -z "$TEMPLATES" ]; then
    echo "No templates to delete (only base template exists)"
    exit 0
fi

COUNT=$(echo "$TEMPLATES" | wc -l)
echo "Found $COUNT templates to delete (keeping base: $BASE_TEMPLATE_ID)"
echo ""

for id in $TEMPLATES; do
    echo "Deleting $id..."
    if e2b template delete "$id" -y 2>&1; then
        echo "  Deleted successfully"
    else
        echo "  Failed to delete (may have paused sandboxes)"
    fi
done

echo ""
echo "Done. Remaining templates:"
e2b template list 2>&1
