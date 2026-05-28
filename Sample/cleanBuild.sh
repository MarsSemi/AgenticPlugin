#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

removed=0

remove_path() {
    local target="$1"
    if [ -e "$target" ] || [ -L "$target" ]; then
        rm -rf "$target"
        printf 'removed %s\n' "$target"
        removed=$((removed + 1))
    fi
}

remove_path "bin"
remove_path "build"
remove_path "dist"
remove_path "out"
remove_path "tmp"
remove_path ".tmp"
remove_path "coverage"
remove_path "sample-service"
remove_path "sample-service.exe"

remove_path "plugins/sample/bin"
remove_path "plugins/sample/runtime"
remove_path "plugins/sample/config.json"
remove_path "plugins/sample/skill/skill-cards.json"

while IFS= read -r path; do
    rm -f "$path"
    printf 'removed %s\n' "${path#"$ROOT_DIR"/}"
    removed=$((removed + 1))
done < <(
    find "$ROOT_DIR" -type f \( \
        -name ".DS_Store" -o \
        -name "*.log" -o \
        -name "*.tmp" -o \
        -name "*.out" -o \
        -name "*.prof" -o \
        -name "*.coverprofile" -o \
        -name "*.test" \
    \)
)

while IFS= read -r path; do
    rm -rf "$path"
    printf 'removed %s\n' "${path#"$ROOT_DIR"/}"
    removed=$((removed + 1))
done < <(
    find "$ROOT_DIR" -type d \( \
        -name ".cache" -o \
        -name ".parcel-cache" -o \
        -name ".turbo" -o \
        -name "node_modules" \
    \) -prune
)

if [ "$removed" -eq 0 ]; then
    printf 'clean: no build artifacts found\n'
else
    printf 'clean: removed %d artifact(s)\n' "$removed"
fi
