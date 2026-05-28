#!/bin/zsh
set -e

SCRIPT_DIR="${0:A:h}"
"$SCRIPT_DIR/build.sh"

echo ""
echo "build finished. Press any key to close this window."
read -k 1 -s
