#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rm -rf "$ROOT_DIR/build" "$ROOT_DIR/dist"
rm -rf "$ROOT_DIR/plugins/email-check/bin" "$ROOT_DIR/plugins/email-check/runtime"
rm -f "$ROOT_DIR/plugins/email-check/config.json" "$ROOT_DIR/plugins/email-check/skill/skill-cards.json"
printf 'cleaned EMAIL Check build/runtime artifacts\n'
