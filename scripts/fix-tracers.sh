#!/bin/bash

# Script to find and replace otel.Tracer declarations with file paths
# Usage: ./replace_tracer.sh [directory]

# Set the search directory (default to current directory if not provided)
SEARCH_DIR="packages/"

# Counter for processed files
processed=0
skipped=0

readonly prefix="github.com/e2b-dev/infra/packages"
readonly search_pattern='var tracer = otel\.Tracer(.*'

echo "Starting tracer replacement in directory: $SEARCH_DIR"
echo "----------------------------------------"

# Find all files containing the pattern
# Using grep to find files first is more efficient than processing all files
while IFS= read -r -d '' file; do
    # Check if file contains the pattern
    if grep -q "$search_pattern" "$file"; then
        # Get the relative path from the search directory
        relative_path=$(realpath --relative-to="$SEARCH_DIR" "$file")
        relative_path="${prefix}/$(dirname "$relative_path")"

        # Perform the replacement
        # Using sed with proper escaping
        full_replace='s/'"${search_pattern}"'/var tracer = otel.Tracer("'"${relative_path//\//\\/}"'")/'
        sed -i "$full_replace" "$file"

        # Check if replacement was successful
        if [ $? -eq 0 ]; then
            echo "✓ Processed: $file"
            ((processed++))
        fi
    else
        ((skipped++))
    fi
done < <(find "$SEARCH_DIR" -type f \( -name "*.go" -o -name "*.js" -o -name "*.ts" -o -name "*.java" -o -name "*.py" \) -print0 2>/dev/null)

echo "----------------------------------------"
echo "Summary:"
echo "  Files processed: $processed"
echo "  Files checked (no matches): $skipped"
echo "  Total files examined: $((processed + skipped))"
