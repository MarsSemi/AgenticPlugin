#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-$(date '+1.%y.%m%d build %H%M')}"
HOST_GOOS="$(go env GOOS)"
HOST_GOARCH="$(go env GOARCH)"
TARGETS=("${HOST_GOOS}/${HOST_GOARCH}" "linux/arm64" "linux/amd64" "windows/amd64")

SAFE_VERSION="$(printf '%s' "$VERSION" | tr ' /:' '___')"
PACKAGE_NAME="sample-plugin_${SAFE_VERSION}"
DIST_DIR="$ROOT_DIR/dist"
STAGE_DIR="$ROOT_DIR/build/$PACKAGE_NAME"
ZIP_PATH="$DIST_DIR/$PACKAGE_NAME.zip"

printf 'build version: %s\n' "$VERSION"
printf 'targets: %s\n' "${TARGETS[*]}"

rm -rf "$ROOT_DIR/build" "$DIST_DIR"
mkdir -p "$STAGE_DIR/plugins" "$STAGE_DIR/website" "$STAGE_DIR/plugins/sample/bin" "$DIST_DIR"

cp -R "$ROOT_DIR/plugins/sample" "$STAGE_DIR/plugins/"
cp -R "$ROOT_DIR/website/sample" "$STAGE_DIR/website/"
cp "$ROOT_DIR/README.md" "$STAGE_DIR/README.md"

rm -rf "$STAGE_DIR/plugins/sample/bin" "$STAGE_DIR/plugins/sample/runtime"
rm -f "$STAGE_DIR/plugins/sample/config.json" "$STAGE_DIR/plugins/sample/skill/skill-cards.json"
mkdir -p "$STAGE_DIR/plugins/sample/bin"

TARGET_ARGS=()
for target in "${TARGETS[@]}"; do
    GOOS_VALUE="${target%%/*}"
    GOARCH_VALUE="${target##*/}"
    BIN_NAME="sample-service_${GOOS_VALUE}_${GOARCH_VALUE}"
    if [ "$GOOS_VALUE" = "windows" ]; then
        BIN_NAME="sample-service_${GOOS_VALUE}_${GOARCH_VALUE}.exe"
    fi
    printf 'building target: %s/%s -> %s\n' "$GOOS_VALUE" "$GOARCH_VALUE" "$BIN_NAME"
    CGO_ENABLED=0 GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" go build -trimpath \
        -ldflags="-s -w -X 'agentic-plugin/sample/src/sampleplugin.SamplePluginVersion=$VERSION'" \
        -o "$STAGE_DIR/plugins/sample/bin/$BIN_NAME" \
        ./src/sampleplugin/service
    TARGET_ARGS+=("${GOOS_VALUE}/${GOARCH_VALUE}=./plugins/sample/bin/${BIN_NAME}")
done

node - "$VERSION" "$STAGE_DIR" "${TARGET_ARGS[@]}" <<'NODE'
const fs = require("fs");
const path = require("path");

const [version, stageDir, ...targetArgs] = process.argv.slice(2);

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function writeJSON(file, value) {
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

const pluginFile = path.join(stageDir, "plugins", "sample", "plugin.json");
const plugin = readJSON(pluginFile);
plugin.version = version;
plugin.entry = plugin.entry || "./plugins/sample/bin/sample-service";
plugin.platform_entries = {};
const binaries = {};
for (const arg of targetArgs) {
  const [target, binary] = arg.split("=");
  if (!target || !binary) continue;
  plugin.platform_entries[target] = binary;
  binaries[target] = binary;
}
writeJSON(pluginFile, plugin);

const configFile = path.join(stageDir, "plugins", "sample", "config.default.json");
const config = readJSON(configFile);
config.version = version;
writeJSON(configFile, config);

writeJSON(path.join(stageDir, "build-info.json"), {
  plugin_id: "sample",
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
