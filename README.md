# yamdview

`yamdview` is a command-line Markdown viewer written in Go. It renders a Markdown file and opens it in your system browser via a local HTTP server.

## Current status

Phase 2 complete: Markdown files are watched and browser views live-reload with full-page reset updates.

## Usage

```sh
make build
./bin/yamdview path/to/file.md
```

This opens your default browser showing the rendered Markdown. Saving the source file refreshes the browser automatically. Use `--no-open` to suppress browser opening:

```sh
./bin/yamdview --no-open README.md
```

### Flags

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--addr` | `127.0.0.1:0` | HTTP bind address (port 0 picks a free port) |
| `--debounce` | `150ms` | File watcher debounce duration |
| `--no-open` | `false` | Do not open the system browser automatically |
| `--export` | _(empty)_ | Export a standalone HTML file (CSS/JS inlined) |
| `--export-view` | _(empty)_ | Viewport target for export: `phone`, `tablet`, `laptop`, `desktop` |

### Standalone export

Generate a self-contained HTML file that can be opened directly in any browser without
running a server:

```sh
./bin/yamdview --export report.html README.md
```

Use `--export-view` to fix the content width for a specific reading context:

```sh
./bin/yamdview --export report.html --export-view tablet README.md
```

Valid values for `--export-view`: `phone` (~22rem), `tablet` (~40rem), `laptop` (~52rem), `desktop` (~62rem).

Press `Ctrl+C` to shut down the server when running without `--export`.

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

- block-level Markdown diffing and DOM patching (Phase 3)
- explicit and Unicode mathematical notation support with local KaTeX assets (Phase 4–5)
- heuristic repair for malformed Markdown tables (Phase 6)
- optional configurable LLM fallback for math/table repairs (Phase 8)
- safe fix persistence with backup or in-place modes (Phase 7)

## Privacy and safety notes

LLM-based repair will be opt-in. Source text will not be sent to any provider unless explicitly enabled or approved by the user.
