#!/bin/sh
# Usage: scripts/build.sh [go build options and packages]
set -eu
cd "$(dirname "$0")/.."
node scripts/prepare-frontend.mjs
go build "$@"
