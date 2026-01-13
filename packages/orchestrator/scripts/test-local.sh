#!/bin/bash
set -e

cd "$(dirname "$0")/.."
DATA_DIR=".local-build"

echo "============================================"
echo "       E2B Local Build - Dedup Test        "
echo "============================================"

# Clean up previous builds
echo ""
echo "Cleaning up previous builds..."
sudo rm -rf "$DATA_DIR/templates/"*

# Create a fresh base build
base_id=$(uuidgen)
echo ""
echo "Creating base build: $base_id"
sudo `which go` run ./cmd/build-template \
    -build "$base_id" \
    -local \
    -data-dir "$DATA_DIR" 2>&1 | grep -E "^(->|✓|❌)" || true

echo ""
echo "============================================"
echo "              BUILD RESULTS                "
echo "============================================"
echo ""

# Show template sizes
echo "Template files in $DATA_DIR/templates/$base_id/:"
echo ""
printf "%-25s %10s\n" "FILE" "SIZE"
printf "%-25s %10s\n" "-------------------------" "----------"

for f in "$DATA_DIR/templates/$base_id/"*; do
    name=$(basename "$f")
    size=$(du -h "$f" | cut -f1)
    printf "%-25s %10s\n" "$name" "$size"
done

echo ""
echo "Memory reduction stats:"
echo "  Total memfile size: 512 MiB (virtual)"

# Get actual memfile diff size
memfile_size=$(du -m "$DATA_DIR/templates/$base_id/memfile" | cut -f1)
echo "  Actual memfile diff: ${memfile_size} MiB"
pct=$((100 - memfile_size * 100 / 512))
echo "  Reduction: ${pct}% (deduped zeros)"

echo ""
echo "Rootfs reduction stats:"
rootfs_virtual=$(stat --format=%s "$DATA_DIR/templates/$base_id/rootfs.ext4" 2>/dev/null || echo "0")
rootfs_virtual_mb=$((rootfs_virtual / 1024 / 1024))
rootfs_actual=$(du -m "$DATA_DIR/templates/$base_id/rootfs.ext4" | cut -f1)
echo "  Total rootfs size: ${rootfs_virtual_mb} MiB (virtual)"
echo "  Actual rootfs diff: ${rootfs_actual} MiB"
if [ "$rootfs_virtual_mb" -gt 0 ]; then
    pct=$((100 - rootfs_actual * 100 / rootfs_virtual_mb))
    echo "  Reduction: ${pct}% (sparse file)"
fi

echo ""
echo "============================================"
echo "                 SUCCESS!                  "
echo "============================================"
