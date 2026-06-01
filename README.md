# yamdview

`yamdview` is a command-line Markdown viewer written in Go. It parses a Markdown file, serves a styled browser view from a local HTTP server, and watches the file for changes — automatically refreshing the rendered output on save.

## Current status

Phases 0–5 complete:

- **Phase 0** — Repository bootstrap with local build caches and vendored dependencies.
- **Phase 1** — Static Markdown-to-browser view via goldmark with embedded CSS/JS.
- **Phase 2** — File watching with debounced live reload over SSE.
- **Phase 3** — (not started) Block-level diffing and partial DOM patching.
- **Phase 4** — Explicit math support (`$...$`, `$$...$$`, `\(...\)`, `\[...\]`, fenced `math` blocks) rendered with vendored KaTeX. No CDN required.
- **Phase 5** — Heuristic Unicode math detection and conversion. LLM-style Unicode equations (operators, Greek letters, superscripts, subscripts, fractions, etc.) are automatically converted to TeX and rendered with KaTeX. The original source file is never modified.

## Quick start

```sh
make deps
make vendor
make build
./bin/yamdview path/to/file.md
```

This opens your default browser showing the rendered Markdown. Saving the source file refreshes the browser automatically.

Press `Ctrl+C` to shut down the server.

## Usage

### Live viewing

```sh
./bin/yamdview README.md
```

### Suppress browser opening

```sh
./bin/yamdview --no-open README.md
```

### Standalone HTML export

Generate a self-contained HTML file (CSS, JS, and KaTeX inlined) that can be opened directly in any browser without running a server:

```sh
./bin/yamdview --export report.html README.md
```

### Fixed-viewport export

Use `--export-view` to fix the content width for a specific reading context:

```sh
./bin/yamdview --export report.html --export-view tablet README.md
```

Valid values: `phone` (~22rem), `tablet` (~40rem), `laptop` (~52rem), `desktop` (~62rem).

### Quick offline inspection

When testing rendering output programmatically, export to a temp file and inspect it — much faster than launching a browser:

```sh
./bin/yamdview --export /tmp/preview.html README.md
grep 'data-tex=' /tmp/preview.html   # check KaTeX elements
```

## Flags

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--addr` | `127.0.0.1:0` | HTTP bind address (port 0 picks a free port) |
| `--debounce` | `150ms` | File watcher debounce duration |
| `--no-open` | `false` | Do not open the system browser automatically |
| `--export` | _(empty)_ | Export standalone HTML to this path (no server) |
| `--export-view` | _(empty)_ | Viewport target for export: `phone`, `tablet`, `laptop`, `desktop` |

## Math support

### Explicit TeX notation

Standard math delimiters are parsed and rendered with vendored KaTeX:

```markdown
Inline: $x^2 + y^2$ or \( \alpha + \beta \)

Display:
$$
E = mc^2
$$

\[
\int_0^1 x^2 dx
\]

Fenced:
```math
\sum_{i=1}^{n} i
```
```

No CDN is required — KaTeX fonts, CSS, and JS are embedded in the binary.

### Unicode math heuristic conversion

LLM output often contains Unicode math without TeX delimiters, for example:

```text
∀x ∈ ℝ, x² ≥ 0
αᵢ = βᵢ + γᵢ
∫₀¹ x² dx = 1/3
```

`yamdview` detects these automatically and converts them to KaTeX-rendered TeX at display time. The original Markdown source file is **never modified** — conversion is render-only.

Supported Unicode categories:

| Category | Examples | TeX output |
| -------- | ------- | ---------- |
| Greek letters | α β γ π ω | `\alpha` `\beta` `\gamma` `\pi` `\omega` |
| Operators | ∀ ∈ ∑ ∫ ≤ ≠ → ∪ ∩ | `\forall` `\in` `\sum` `\int` `\le` `\neq` `\to` `\cup` `\cap` |
| Superscripts | ² ³ ⁿ | `^{2}` `^{3}` `^{n}` |
| Subscripts | ₀ ₁ ᵢ | `_{0}` `_{1}` `_{i}` |
| Blackboard bold | ℝ ℕ ℤ ℂ | `\mathbb{R}` `\mathbb{N}` `\mathbb{Z}` `\mathbb{C}` |
| Fractions | ½ ⅓ ¼ | `\frac{1}{2}` `\frac{1}{3}` `\frac{1}{4}` |
| Symbols | √ · ° ∞ ∅ | `\sqrt{}` `\cdot` `{}^\circ` `\infty` `\emptyset` |

**Safety properties:**

- Content inside code fences and inline code is never modified.
- Text that already contains TeX delimiters (`$$`, `\(`, `\[`) is left untouched (idempotent).
- Prose without Unicode math characters is never affected.
- Confidence scoring prevents false-positive conversion of ordinary text with accented characters.

## Development

Requires Go 1.23.0 or newer. Build and module caches are kept local to the project.

```sh
make deps      # download modules to .cache/gomod
make vendor    # vendor dependencies
make test      # run tests with local caches
make build     # build to bin/yamdview
make check     # lint + test
make clean     # remove bin/, .cache/, dist/, .tmp/
```

Or source the helper environment:

```sh
. ./scripts/env.sh
go test ./...
```

Local generated files are ignored under `.cache/`, `bin/`, `dist/`, and `.tmp/`.

## Roadmap

| Phase | Description | Status |
| ----- | ----------- | ------ |
| 0 | Repository bootstrap | ✅ Complete |
| 1 | Static Markdown to browser view | ✅ Complete |
| 2 | File watching and live reload | ✅ Complete |
| 3 | Block segmentation and partial DOM patching | Planned |
| 4 | KaTeX and explicit math support | ✅ Complete |
| 5 | Unicode math heuristic conversion | ✅ Complete |
| 6 | Table heuristic detection and repair | Planned |
| 7 | Safe fix persistence | Planned |
| 8 | LLM provider abstraction and fallback | Planned |
| 9 | UX, performance, and polish | Planned |
| 10 | Packaging and documentation | Planned |

## Privacy and safety notes

- The local server binds to `127.0.0.1` by default.
- LLM-based repair will be opt-in. Source text will not be sent to any provider unless explicitly enabled or approved by the user.
- Heuristic math and table conversions are render-only by default — the original Markdown file is never modified unless the user explicitly opts in via `--write-fixes`.
