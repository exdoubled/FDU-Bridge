#!/usr/bin/env bash
# Builds the desktop GUI and core on Linux or macOS; see gui/build.sh for options.
exec bash "$(dirname "$0")/gui/build.sh" "$@"
