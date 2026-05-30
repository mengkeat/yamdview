# yamdview

`yamdview` is planned as a command-line Markdown viewer written in Go. It will open a local browser view for a Markdown file, watch the source file for changes, and eventually patch only changed DOM blocks for a smooth live-preview experience.

The project is currently in phase 0: repository bootstrap and CLI path validation.

## Current usage

```sh
make build
./bin/yamdview path/to/file.md
```

At this stage the command validates that the Markdown file exists and prints a bootstrap message. Browser rendering and live updates will be added in later phases.

## Development

Requires Go 1.23.0 or newer.

This repository keeps Go build and module caches local to the project folder.

```sh
make deps
make vendor
make test
make build
```

Or source the helper environment file:

```sh
. ./scripts/env.sh
go test ./...
```

Local generated files are ignored under `.cache/`, `bin/`, `dist/`, and `.tmp/`.

## Roadmap

Planned capabilities include:

- local HTTP browser viewer with automatic browser opening
- file watching and live browser updates
- block-level Markdown diffing and DOM patching
- explicit and Unicode mathematical notation support with local KaTeX assets
- heuristic repair for malformed Markdown tables
- optional configurable LLM fallback for math/table repairs
- safe fix persistence with backup or in-place modes

## Privacy and safety notes

LLM-based repair will be opt-in. Source text should not be sent to any provider unless explicitly enabled or approved by the user.
