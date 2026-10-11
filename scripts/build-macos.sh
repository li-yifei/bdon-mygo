#!/usr/bin/env bash
# The macOS release (Apple silicon): see scripts/build-release.sh.
exec bash "$(dirname "$0")/build-release.sh" darwin/arm64 "$@"
