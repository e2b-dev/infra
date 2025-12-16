#!/bin/bash

targetPath="{{ .TargetPath }}"
sourcePath="{{ .SourcePath }}"
owner="{{ .Owner }}"
permissions="{{ .Permissions }}"

workdir="{{ .Workdir }}"
user="{{ .User }}"

# Fill the workdir with user home directory if empty
if [ -z "${workdir}" ]; then
    # Use the user's home directory
    workdir=$(getent passwd "$user" | cut -d: -f6)
fi
cd "$workdir" || exit 1

# Get the parent folder of the source file/folder
sourceFolder="$(dirname "$sourcePath")"

# Set targetPath relative to current working directory if not absolute
inputPath="$targetPath"
if [[ "$inputPath" = /* ]]; then
    targetPath="$inputPath"
else
    targetPath="$(pwd)/$inputPath"
fi

cd "$sourceFolder" || exit 1

# Get the first entry (file, directory, or symlink)
entry=$(ls -A | head -n 1)

if [ -z "$entry" ]; then
    echo "Error: sourceFolder is empty"
    exit 1
fi

# Check type BEFORE applying ownership/permissions to avoid dereferencing symlinks
if [ -L "$entry" ]; then
    # It's a symlink – create parent folders and move+rename it to the exact path
    mkdir -p "$(dirname "$targetPath")"
    # Change ownership of the symlink itself (not the target)
    chown -h "$owner" "$entry"
    # Note: chmod on symlinks affects the target, not the link itself in most systems
    # We skip chmod for symlinks as it's typically not meaningful
    mv "$entry" "$targetPath"
elif [ -f "$entry" ]; then
    # It's a file – create parent folders and move+rename it to the exact path
    chown "$owner" "$entry"
    if [ -n "$permissions" ]; then
        chmod "$permissions" "$entry"
    fi
    mkdir -p "$(dirname "$targetPath")"
    mv "$entry" "$targetPath"
elif [ -d "$entry" ]; then
    # It's a directory – apply ownership/permissions recursively, then move contents
    chown -R "$owner" "$entry"
    if [ -n "$permissions" ]; then
        chmod -R "$permissions" "$entry"
    fi
    mkdir -p "$targetPath"
    # Move all contents including hidden files
    find "$entry" -mindepth 1 -maxdepth 1 -exec mv {} "$targetPath/" \;
else
    echo "Error: entry is neither file, directory, nor symlink"
    exit 1
fi
