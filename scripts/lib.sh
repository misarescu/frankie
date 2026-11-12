# shellcheck shell=bash
# Shared helpers for scripts/*.sh. Source it; do not execute it.

# has_files DIR GLOB: succeeds if DIR exists and contains a file matching GLOB.
has_files() {
	[ -d "$1" ] && find "$1" -type f -name "$2" -print -quit | grep -q .
}
