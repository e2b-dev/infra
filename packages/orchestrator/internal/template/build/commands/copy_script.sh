#!/bin/bash
bb={{ .BusyBox }}

targetPath="{{ .TargetPath }}"
sourcePath="{{ .SourcePath }}"
owner="{{ .Owner }}"
permissions="{{ .Permissions }}"

workdir="{{ .Workdir }}"
user="{{ .User }}"

# Fill the workdir with user home directory if empty
if $bb [ -z "${workdir}" ]; then
    # Use the user's home directory
    # Support both username and numeric UID lookups
    workdir=$($bb awk -F: -v u="$user" '$1 == u || $3 == u { print $6 }' /etc/passwd)
fi
cd "$workdir" || exit 1

# Get the parent folder of the source file/folder
sourceFolder="$($bb dirname "$sourcePath")"

# Set targetPath relative to current working directory if not absolute
inputPath="$targetPath"
if [[ "$inputPath" = /* ]]; then
    targetPath="$inputPath"
else
    targetPath="$(pwd)/$inputPath"
fi

cd "$sourceFolder" || exit 1

# Get the first entry (file, directory, or symlink)
entry=$($bb ls -A | $bb head -n 1)

if $bb [ -z "$entry" ]; then
    echo "Error: sourceFolder is empty"
    exit 1
fi

# Check type BEFORE applying ownership/permissions to avoid dereferencing symlinks
if $bb [ -L "$entry" ]; then
    # It's a symlink – create parent folders and move+rename it to the exact path
    $bb mkdir -p "$($bb dirname "$targetPath")"
    # Change ownership of the symlink itself (not the target)
    $bb chown -h "$owner" "$entry"
    # Note: chmod on symlinks affects the target, not the link itself in most systems
    # We skip chmod for symlinks as it's typically not meaningful
    $bb mv "$entry" "$targetPath"
elif $bb [ -f "$entry" ]; then
    # It's a file – create parent folders and move+rename it to the exact path
    $bb chown "$owner" "$entry"
    if $bb [ -n "$permissions" ]; then
        $bb chmod "$permissions" "$entry"
    fi
    $bb mkdir -p "$($bb dirname "$targetPath")"
    $bb mv "$entry" "$targetPath"
elif $bb [ -d "$entry" ]; then
    # It's a directory – apply ownership/permissions recursively, then move contents
    $bb chown -R "$owner" "$entry"
    if $bb [ -n "$permissions" ]; then
        $bb chmod -R "$permissions" "$entry"
    fi
    $bb mkdir -p "$targetPath"
    # Move all contents including hidden files
    $bb find "$entry" -mindepth 1 -maxdepth 1 -exec $bb mv {} "$targetPath/" \;
else
    echo "Error: entry is neither file, directory, nor symlink"
    exit 1
fi
