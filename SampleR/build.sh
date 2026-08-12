#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

source "$HOME/.cargo/env" 2>/dev/null || true

VERSION="${1:-$(date '+1.%y.%m%d build %H%M')}"
HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
HOST_ARCH="$(uname -m)"

case "$HOST_OS/$HOST_ARCH" in
  darwin/arm64) HOST_TARGET="aarch64-apple-darwin"; HOST_KEY="darwin/arm64" ;;
  darwin/x86_64) HOST_TARGET="x86_64-apple-darwin"; HOST_KEY="darwin/amd64" ;;
  linux/aarch64|linux/arm64) HOST_TARGET="aarch64-unknown-linux-gnu"; HOST_KEY="linux/arm64" ;;
  linux/x86_64) HOST_TARGET="x86_64-unknown-linux-gnu"; HOST_KEY="linux/amd64" ;;
  mingw*/*|msys*/*|cygwin*/*) HOST_TARGET="x86_64-pc-windows-msvc"; HOST_KEY="windows/amd64" ;;
  *) HOST_TARGET="$(rustc -vV | awk '/host:/ {print $2}')"; HOST_KEY="$HOST_OS/$HOST_ARCH" ;;
esac

TARGET_SPECS=(
  "$HOST_KEY|$HOST_TARGET|sampler-service_${HOST_KEY%/*}_${HOST_KEY#*/}"
  "linux/arm64|aarch64-unknown-linux-gnu|sampler-service_linux_arm64"
  "linux/amd64|x86_64-unknown-linux-gnu|sampler-service_linux_amd64"
  "windows/amd64|x86_64-pc-windows-gnu|sampler-service_windows_amd64.exe"
)

SAFE_VERSION="$(printf '%s' "$VERSION" | tr ' /:' '___')"
PACKAGE_NAME="sample-r-plugin_${SAFE_VERSION}"
DIST_DIR="$ROOT_DIR/dist"
STAGE_DIR="$ROOT_DIR/build/$PACKAGE_NAME"
ZIP_PATH="$DIST_DIR/$PACKAGE_NAME.zip"

printf 'build version: %s\n' "$VERSION"

rm -rf "$ROOT_DIR/build" "$DIST_DIR"
mkdir -p "$STAGE_DIR/plugins" "$STAGE_DIR/website" "$STAGE_DIR/plugins/sample-r/bin" "$DIST_DIR"

cp -R "$ROOT_DIR/plugins/sample-r" "$STAGE_DIR/plugins/"
cp -R "$ROOT_DIR/website/sample-r" "$STAGE_DIR/website/"
cp "$ROOT_DIR/README.md" "$STAGE_DIR/README.md"
cp "$ROOT_DIR/MCP.md" "$STAGE_DIR/MCP.md"

rm -rf "$STAGE_DIR/plugins/sample-r/bin" "$STAGE_DIR/plugins/sample-r/runtime"
rm -f "$STAGE_DIR/plugins/sample-r/config.json" "$STAGE_DIR/plugins/sample-r/skill/skill-cards.json"
mkdir -p "$STAGE_DIR/plugins/sample-r/bin"

TARGET_ARGS=()
BUILT_TARGETS=()
has_built_target() {
  local needle="$1"
  local item
  if [ "${#BUILT_TARGETS[@]}" -eq 0 ]; then
    return 1
  fi
  for item in "${BUILT_TARGETS[@]}"; do
    if [ "$item" = "$needle" ]; then
      return 0
    fi
  done
  return 1
}

build_target() {
  local target_key="$1"
  local target_triple="$2"
  local log_file="$3"

  rustup target add "$target_triple" >/dev/null 2>&1 || true
  if [ "$target_key" = "$HOST_KEY" ]; then
    cargo build --release --target "$target_triple" >"$log_file" 2>&1
    return $?
  fi

  if command -v cargo-zigbuild >/dev/null 2>&1; then
    cargo zigbuild --release --target "$target_triple" >"$log_file" 2>&1
    return $?
  fi

  printf 'warn: skip target %s; install zig and cargo-zigbuild to enable Rust cross compilation\n' "$target_key" >&2
  return 2
}

for spec in "${TARGET_SPECS[@]}"; do
  IFS='|' read -r target_key target_triple bin_name <<< "$spec"
  if has_built_target "$target_key"; then
    continue
  fi
  BUILT_TARGETS+=("$target_key")
  printf 'building target: %s (%s) -> %s\n' "$target_key" "$target_triple" "$bin_name"
  log_file="$ROOT_DIR/build/${target_key//\//_}.log"
  if build_target "$target_key" "$target_triple" "$log_file"; then
    source_bin="$ROOT_DIR/target/$target_triple/release/sampler-service"
    if [[ "$target_key" == windows/* ]]; then
      source_bin="$source_bin.exe"
    fi
    cp "$source_bin" "$STAGE_DIR/plugins/sample-r/bin/$bin_name"
    TARGET_ARGS+=("${target_key}=./plugins/sample-r/bin/${bin_name}")
  else
    status=$?
    if [ "$status" -ne 2 ]; then
      printf 'warn: skip target %s; build failed, last log lines follow\n' "$target_key" >&2
      tail -n 30 "$log_file" >&2 || true
    fi
  fi
done

if [ "${#TARGET_ARGS[@]}" -eq 0 ]; then
  printf 'error: no Rust binaries were built\n' >&2
  exit 1
fi

node - "$VERSION" "$STAGE_DIR" "${TARGET_ARGS[@]}" <<'NODE'
const fs = require("fs");
const path = require("path");

const [version, stageDir, ...targetArgs] = process.argv.slice(2);
const readJSON = (file) => JSON.parse(fs.readFileSync(file, "utf8"));
const writeJSON = (file, value) => fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);

const pluginFile = path.join(stageDir, "plugins", "sample-r", "plugin.json");
const plugin = readJSON(pluginFile);
plugin.version = version;
plugin.platform_entries = {};
const binaries = {};
for (const arg of targetArgs) {
  const [target, binary] = arg.split("=");
  if (!target || !binary) continue;
  plugin.platform_entries[target] = binary;
  binaries[target] = binary;
}
writeJSON(pluginFile, plugin);

const configFile = path.join(stageDir, "plugins", "sample-r", "config.default.json");
const config = readJSON(configFile);
config.version = version;
writeJSON(configFile, config);

writeJSON(path.join(stageDir, "build-info.json"), {
  plugin_id: "sample-r",
  language: "rust",
  version,
  target: "multi",
  targets: Object.keys(binaries),
  binaries,
  platform_entries: plugin.platform_entries,
  created_at: new Date().toISOString()
});
NODE

(
  cd "$STAGE_DIR"
  zip -qr "$ZIP_PATH" .
)

printf 'package: %s\n' "$ZIP_PATH"
