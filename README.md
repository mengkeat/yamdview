# yamdview

`yamdview` is a command-line Markdown viewer written in Go. It renders a Markdown file and opens it in your system browser via a local HTTP server.

## Current status

Phase 1 complete: static Markdown to browser view. File watching and live updates arrive in Phase 2.

## Usage

```sh
make build
./bin/yamdview path/to/file.md
```

This opens your default browser showing the rendered Markdown. Use `--no-open` to suppress browser opening:

```sh
./bin/yamdview --no-open README.md
```

### Flags

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--addr` | `127.0.0.1:0` | HTTP bind address (port 0 picks a free port) |
| `--no-open` | `false` | Do not open the system browser automatically |

Press `Ctrl+C` to shut down the server.

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

- file watching and live browser updates (Phase 2)
- block-level Markdown diffing and DOM patching (Phase 3)
- explicit and Unicode mathematical notation support with local KaTeX assets (Phase 4–5)
- heuristic repair for malformed Markdown tables (Phase 6)
- optional configurable LLM fallback for math/table repairs (Phase 8)
- safe fix persistence with backup or in-place modes (Phase 7)

## Privacy and safety notes

LLM-based repair will be opt-in. Source text will not be sent to any provider unless explicitly enabled or approved by the user.
