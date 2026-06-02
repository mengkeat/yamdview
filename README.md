<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://img.shields.io/badge/YAMDView-1a1814?style=for-the-badge&logo=markdown&logoColor=faf7f0&labelColor=1a1814">
    <img alt="YAMDView" src="https://img.shields.io/badge/YAMDView-1e1b16?style=for-the-badge&logo=markdown&logoColor=faf7f0&labelColor=4a4540">
  </picture>
</p>

<p align="center">
  <strong>Yet Another Markdown Viewer</strong> &mdash; a single-binary, offline-first<br>
  Markdown preview tool with live reload, KaTeX math, and intelligent Unicode conversion.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/mengkeat/yamdview"><img src="https://pkg.go.dev/badge/github.com/mengkeat/yamdview.svg" alt="Go Reference"></a>
  <a href="https://github.com/mengkeat/yamdview/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://github.com/mengkeat/yamdview"><img src="https://img.shields.io/github/go-mod/go-version/mengkeat/yamdview" alt="Go Version"></a>
  <a href="https://goreportcard.com/report/github.com/mengkeat/yamdview"><img src="https://goreportcard.com/badge/github.com/mengkeat/yamdview" alt="Go Report Card"></a>
</p>

---

## Table of Contents

- [Features](#features)
- [Why YAMDView?](#why-yamdview)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Usage](#usage)
  - [Live viewing](#live-viewing)
  - [Flags](#flags)
  - [Standalone HTML export](#standalone-html-export)
  - [Fixed-viewport export](#fixed-viewport-export)
- [Math Support](#math-support)
  - [Explicit TeX notation](#explicit-tex-notation)
  - [Unicode math heuristic conversion](#unicode-math-heuristic-conversion)
- [Table Repair](#table-repair)
- [Design & Theme](#design--theme)
- [Development](#development)
- [Roadmap](#roadmap)
- [Privacy & Safety](#privacy--safety)
- [Contributing](#contributing)
- [License](#license)
- [Acknowledgments](#acknowledgments)

## Features

> *No Node.js. No Python. No CDN. Just one binary.*

- **🖥 Live Preview** &mdash; Render Markdown in your browser; auto-refreshes on save via Server-Sent Events (SSE) with partial DOM patching for instant updates.
- **📐 KaTeX Math** &mdash; Inline (`$...$`), display (`$$...$$`), LaTeX-style (`\(...\)`, `\[...\]`), and fenced `math` blocks &mdash; all rendered with fully vendored KaTeX. Zero network calls.
- **🔣 Unicode Math Detection** &mdash; Automatically converts LLM-style Unicode equations (`∀x ∈ ℝ, x² ≥ 0`) to rendered TeX at display time. The source file is **never modified**.
- **📊 Table Repair** &mdash; Heuristically detects and fixes malformed pipe tables (misaligned columns, missing separators, code pipes).
- **📤 Offline Export** &mdash; Generate self-contained HTML files with inlined CSS, JS, and KaTeX. Open in any browser, no server needed.
- **🎨 Paper & Ink Theme** &mdash; A warm, distinctive reading environment with Crimson Pro typography, paper-toned backgrounds, and ink-like text colors. [See below](#design--theme).
- **🔒 Privacy-First** &mdash; Binds to `127.0.0.1` by default. Zero telemetry, zero network requests during preview.
- **🪶 Single Binary** &mdash; Compiles to one static binary with all assets embedded. No runtime dependencies.

## Why YAMDView?

Most Markdown preview tools require a heavy runtime (Node.js, Python, or an editor), rely on CDNs for math rendering, or lack live reload. YAMDView takes a different approach:

- **Single static binary** — no runtime dependencies. Compile once, run anywhere.
- **Offline by design** — KaTeX, CSS, and JS are embedded at compile time. Zero CDN calls.
- **Unicode math detection** — automatically renders LLM-style equations (`∀x ∈ ℝ, x² ≥ 0`) as proper KaTeX.
- **Table repair** — heuristically fixes malformed pipe tables common in LLM output.
- **Partial DOM patching** — edits stream as fine-grained patches over SSE, not full-page reloads.
- **Self-contained export** — generate a single `.html` file with everything inlined for offline reading.
- **Paper & Ink theme** — a warm, distinctive reading environment with Crimson Pro typography and dark mode.

YAMDView was built for writing and iterating on Markdown documents that contain significant mathematical notation — especially when that notation arrives as raw Unicode from LLM outputs. You focus on the content; the viewer handles the rendering.

## Installation

### Via `go install` (recommended)

```sh
go install github.com/mengkeat/yamdview/cmd/yamdview@latest
```

Requires Go 1.23 or newer. The binary is installed to `$(go env GOPATH)/bin`.

### Build from source

```sh
git clone https://github.com/mengkeat/yamdview.git
cd yamdview
make deps vendor build
./bin/yamdview --help
```

### Binary releases

Pre-built binaries for Linux, macOS, and Windows are available on the [GitHub Releases](https://github.com/mengkeat/yamdview/releases) page.

## Quick Start

```sh
yamdview README.md
```

This opens your default browser showing the rendered Markdown in the Paper & Ink theme. Save the source file to trigger an instant, patched refresh.

Press `Ctrl+C` to shut down the server.

## Usage

### Live viewing

```sh
yamdview notes.md                   # preview with live reload
yamdview --no-open notes.md         # start server without opening browser
yamdview --addr 0.0.0.0:8080 notes.md  # bind to a specific address
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `127.0.0.1:0` | HTTP bind address (port `0` picks a free port) |
| `--debounce` | `150ms` | File watcher debounce duration (Go duration format) |
| `--no-open` | `false` | Don't open the system browser automatically |
| `--export` | *(empty)* | Export self-contained HTML to this path (skips server) |
| `--export-view` | *(empty)* | Export viewport target: `phone`, `tablet`, `laptop`, `desktop` |
| `--write-fixes` | `never` | Persist heuristic fixes: `never`, `backup`, `in-place` |
| `--backup-dir` | *(empty)* | Directory for backup files when `--write-fixes=backup` (default: source directory) |

### Standalone HTML export

```sh
yamdview --export report.html README.md
```

Produces a single `.html` file with all CSS, JS, and KaTeX inlined &mdash; open it in any browser, no server required.

### Fixed-viewport export

Use `--export-view` to target a specific reading surface:

```sh
yamdview --export report.html --export-view tablet README.md
```

| Value | Max width | Best for |
|-------|-----------|----------|
| `phone` | ~22rem | Mobile screenshots |
| `tablet` | ~40rem | iPad / note-taking |
| `laptop` | ~52rem | Standard laptop display |
| `desktop` | ~62rem | Large monitors |

### Quick offline inspection

When testing rendering output programmatically, export to a temp file &mdash; much faster than launching a browser:

```sh
yamdview --export /tmp/preview.html README.md
grep 'data-tex=' /tmp/preview.html   # check KaTeX elements
```

## Math Support

### Explicit TeX notation

Standard math delimiters are rendered with vendored KaTeX (no CDN, no network):

````markdown
Inline: $x^2 + y^2$ or \( \alpha + \beta \)

Display:
$$
E = mc^2
$$

\[
\int_0^1 x^2 \, dx
\]

Fenced:
```math
\sum_{i=1}^{n} i = \frac{n(n+1)}{2}
```
````

All KaTeX fonts, CSS, and JS are embedded in the binary at compile time. Offline by default.

### Unicode math heuristic conversion

LLM output often contains Unicode math without TeX delimiters:

```text
∀x ∈ ℝ, x² ≥ 0
αᵢ = βᵢ + γᵢ
∫₀¹ x² dx = 1/3
```

YAMDView detects these patterns automatically and converts them to KaTeX-rendered TeX at display time. The original source file is **never modified** &mdash; conversion is render-only.

**Supported Unicode categories:**

| Category | Examples | TeX output |
|----------|----------|-------------|
| Greek letters | `α β γ π ω` | `\alpha` `\beta` `\gamma` `\pi` `\omega` |
| Operators | `∀ ∈ ∑ ∫ ≤ ≠ → ∪ ∩` | `\forall` `\in` `\sum` `\int` `\le` `\neq` `\to` `\cup` `\cap` |
| Superscripts | `² ³ ⁿ` | `^{2}` `^{3}` `^{n}` |
| Subscripts | `₀ ₁ ᵢ` | `_{0}` `_{1}` `_{i}` |
| Blackboard bold | `ℝ ℕ ℤ ℂ` | `\mathbb{R}` `\mathbb{N}` `\mathbb{Z}` `\mathbb{C}` |
| Fractions | `½ ⅓ ¼` | `\frac{1}{2}` `\frac{1}{3}` `\frac{1}{4}` |
| Symbols | `√ · ° ∞ ∅` | `\sqrt{}` `\cdot` `{}^\circ` `\infty` `\emptyset` |

**Safety guarantees:**

- Content inside code fences and inline code is ignored.
- Text already containing `$`, `$$`, `\(`, or `\[` delimiters is left untouched (idempotent).
- Ordinary prose without Unicode math characters is never affected.
- Confidence scoring prevents false-positive conversion of accented text.

## Table Repair

YAMDView can detect and heuristically repair malformed pipe tables &mdash; a common issue in LLM-generated Markdown where columns are misaligned, separator rows are incomplete, or code pipes (`|`) appear inside cells.

Like Unicode math conversion, table repair is **render-only** by default. The source file is not modified.

**Example:** A broken table with missing alignment separators:

```markdown
| Name | Value | Notes |
| foo  | 42    | ok    |
| bar  | 99    | n/a   |
```

YAMDView inserts the missing alignment row and renders a properly formatted table.

### Opting in to source modifications

By default, both Unicode math conversion and table repair affect only the rendered view. To persist the repaired Markdown back to the source file, set `--write-fixes`:

| Mode | Effect |
|------|--------|
| `never` (default) | Renders fixes; the source file is never modified |
| `backup` | Creates a timestamped `file.md.bak-YYYYMMDD-HHMMSS` and atomically rewrites the source |
| `in-place` | Atomically rewrites the source in place (no backup) |

```sh
# Atomic rewrite with a timestamped backup in the same directory.
yamdview --write-fixes=backup notes.md

# In-place rewrite, no backup (use with care; rely on your own VCS).
yamdview --write-fixes=in-place notes.md
```

Patches are validated against the current file contents before they are written. If the file changed since the patches were computed, the write is rejected and the source is left untouched. Pass `--backup-dir <path>` to keep backups in a dedicated directory.

## Design & Theme

YAMDView ships with the **Paper & Ink** theme &mdash; a warm, tactile reading environment that celebrates the materiality of text:

- **Crimson Pro** for body text &mdash; a Garamond-inspired serif with excellent readability
- **JetBrains Mono** for code &mdash; crisp, modern, and ligature-aware
- **Paper-toned backgrounds** (`#faf7f0`) with ink-like text colors (`#1e1b16`)
- **Muted vermillion accents** for links, headings, and interactive elements
- **Dark mode** support via `prefers-color-scheme`, with deep warm-gray backgrounds
- **KaTeX math** rendered in matching ink tones for visual coherence

All fonts are loaded from Google Fonts on first view. The exported HTML inlines them for offline use.

## Development

Requires Go 1.23.0 or newer. Build and module caches are localised to the project directory under `.cache/`.

```sh
make deps       # download modules to .cache/gomod
make vendor     # vendor dependencies
make test       # run tests with local caches
make test-race  # run tests with race detector
make build      # build to bin/yamdview
make check      # lint + test (what CI runs)
make fmt        # format all Go source files
make clean      # remove bin/, .cache/, dist/, .tmp/
```

Or source the helper environment for ad-hoc commands:

```sh
. ./scripts/env.sh
go test ./...
```

Linting uses `golangci-lint`, formatting uses `gofumpt` + `goimports`. See [LINT-FORMAT.md](LINT-FORMAT.md) for editor integration and CI setup.

### Project structure

```
yamdview/
├── cmd/yamdview/        # main entry point
├── internal/
│   ├── app/             # application lifecycle orchestration
│   ├── browser/         # cross-platform browser opening
│   ├── cli/             # flag parsing and configuration
│   ├── document/        # block segmentation, snapshot, and diff
│   ├── markdown/        # goldmark renderer configuration
│   ├── mathfix/         # Unicode math detection and TeX conversion
│   ├── server/          # HTTP server, SSE, and export
│   ├── tablefix/        # heuristic table detection and repair
│   └── watcher/         # file watching with debounce
├── web/                 # embedded HTML, CSS, JS, and KaTeX assets
├── testdata/            # test fixtures
├── scripts/             # helper shell scripts
└── vendor/              # vendored Go dependencies
```

## Roadmap

| Phase | Description | Status |
|-------|-------------|--------|
| 0 | Repository bootstrap | ✅ Complete |
| 1 | Static Markdown to browser view | ✅ Complete |
| 2 | File watching and live reload | ✅ Complete |
| 3 | Block segmentation and partial DOM patching | ✅ Complete |
| 4 | KaTeX and explicit math support | ✅ Complete |
| 5 | Unicode math heuristic conversion | ✅ Complete |
| 6 | Table heuristic detection and repair | ✅ Complete |
| 7 | Safe fix persistence (`--write-fixes`) | ✅ Complete |
| 8 | LLM provider abstraction and fallback | Planned |
| 9 | UX, performance, and polish | Planned |
| 10 | Packaging and documentation | In progress |

## Privacy & Safety

YAMDView is designed to be safe by default:

- **Local-only server** &mdash; binds to `127.0.0.1` by default. Remote access requires an explicit `--addr` flag.
- **No telemetry** &mdash; the binary makes zero outbound network requests during normal operation (fonts are loaded by the *browser*, not the binary).
- **Opt-in modifications** &mdash; Unicode math conversion and table repair are render-only. The source file is never modified unless you explicitly opt in via `--write-fixes=backup` or `--write-fixes=in-place`. The default is `never`.
- **LLM provider opt-in** &mdash; Future LLM-based repair features will require explicit user approval before any source text is sent to a provider.

## Contributing

Contributions are welcome! Please:

1. Open an issue to discuss changes before submitting a PR.
2. Run `make fmt && make check` to ensure formatting and tests pass.
3. Follow existing conventions for test coverage (`go test ./...`).

For editor setup and CI configuration, see [LINT-FORMAT.md](LINT-FORMAT.md).

## License

MIT &mdash; see [LICENSE](LICENSE) for details.

## Acknowledgments

YAMDView is built on excellent open-source libraries:

- [**goldmark**](https://github.com/yuin/goldmark) by Yusuke Inuzuka &mdash; a beautiful, extensible Markdown parser for Go
- [**KaTeX**](https://katex.org) by Khan Academy &mdash; the fastest math typesetting library for the web
- [**fsnotify**](https://github.com/fsnotify/fsnotify) &mdash; cross-platform file system notifications
- [**Crimson Pro**](https://fonts.google.com/specimen/Crimson+Pro) by Jacques Le Bailly &mdash; the elegant serif typeface
- [**JetBrains Mono**](https://www.jetbrains.com/lp/mono/) &mdash; the crisp monospace font for code
