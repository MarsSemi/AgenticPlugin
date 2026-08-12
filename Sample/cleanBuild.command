#!/bin/zsh
set -e

SCRIPT_DIR="${0:A:h}"
"$SCRIPT_DIR/cleanBuild.sh"

echo ""
echo "cleanBuild finished. Press any key to close this window."
read -k 1 -s
