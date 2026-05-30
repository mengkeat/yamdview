#!/usr/bin/env sh
# Source this file to keep Go caches local to the repository.

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

export GOMODCACHE="$ROOT_DIR/.cache/gomod"
export GOCACHE="$ROOT_DIR/.cache/gobuild"
export GOPATH="$ROOT_DIR/.cache/gopath"
export GOFLAGS="-mod=vendor"
