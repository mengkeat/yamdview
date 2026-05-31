# Lint & Format Setup

## Overview

This project enforces code quality through automated linting and formatting. The toolchain is wired into the `Makefile` so CI, editors, and AI agents all use the same commands.

## Toolchain

| Tool | Version | Purpose |
|------|---------|---------|
| [golangci-lint](https://golangci-lint.run) | v1.64.8 | Aggregated linter runner (20+ linters) |
| [gofumpt](https://github.com/mvdan/gofumpt) | v0.10.0 | Stricter `gofmt` with extra formatting rules |
| [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) | v0.28.0 | Import sorting + `gofmt` |
| [.editorconfig](https://editorconfig.org) | — | Cross-editor whitespace/charset consistency |

All tools are installed to `$(go env GOPATH)/bin` via:

```sh
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install mvdan.cc/gofumpt@latest
go install golang.org/x/tools/cmd/goimports@latest
```

## Makefile targets

```sh
make lint          # Run golangci-lint (read-only)
make fmt           # Auto-format all .go files (gofumpt + goimports)
make check         # lint + test (what CI runs)
make test          # Run all tests
make test-race     # Run tests with race detector
```

Run `make fmt && make check` before committing.

## Enabled linters

### Core (always useful, low noise)

| Linter | What it catches |
|--------|----------------|
| `errcheck` | Unchecked error returns |
| `gosimple` | Simplify-able code |
| `govet` | Suspicious constructs (official `go vet`) |
| `ineffassign` | Ineffectual assignments |
| `staticcheck` | Deep static analysis (buggy patterns, deprecated APIs) |
| `unused` | Unused code (variables, functions, types) |

### Quality

| Linter | What it catches |
|--------|----------------|
| `gocritic` | Opinionated Go style checks |
| `gocyclo` | Functions with cyclomatic complexity > 15 |
| `gofumpt` | Code not matching `gofumpt` formatting rules |
| `goimports` | Unsorted imports, missing/extra imports |
| `misspell` | Spelling mistakes in identifiers and comments |
| `nilerr` | Functions returning `nil` instead of an error |
| `prealloc` | Slices that could be pre-allocated |
| `revive` | Drop-in `golint` replacement (exported docs, naming) |
| `tparallel` | Tests that can use `t.Parallel()` but don't |
| `unconvert` | Unnecessary type conversions |
| `unparam` | Unused function parameters |
| `usestdlibvars` | Magic strings/numbers where stdlib constants exist |
| `wastedassign` | Assignments to variables before reassignment |

## Configuration files

### `.golangci.yml`

Located at the repo root. Key settings:

- `modules-download-mode: vendor` — respects vendored dependencies
- `local-prefixes: github.com/mengkeat/yamdview` — groups project imports separately
- `gofumpt.extra-rules: true` — enables all `gofumpt` rules beyond `gofmt`
- `gocyclo.min-complexity: 15` — flags functions with cyclomatic complexity > 15
- Excludes `.cache/` and `vendor/` directories

### `.editorconfig`

Ensures consistent whitespace across editors (VS Code, GoLand, Vim, etc.):

- All files: tabs (size 4), LF endings, UTF-8, trailing whitespace trimmed, final newline
- YAML/JSON: spaces (size 2)
- Markdown: trailing whitespace NOT trimmed (significant in Markdown)

## Editor integration

### VS Code

Add to `.vscode/settings.json`:

```json
{
  "go.formatTool": "gofumpt",
  "go.lintTool": "golangci-lint",
  "go.lintFlags": ["--fast"],
  "editor.formatOnSave": true,
  "go.useLanguageServer": true,
  "gopls": {
    "formatting.gofumpt": true
  }
}
```

### GoLand / IntelliJ

- Enable `File | Settings | Editor | Code Style | Enable EditorConfig support`
- Set `gofumpt` as the File Watcher or configure the Go plugin to use it

### Vim / Neovim

With `vim-go`:
```vim
let g:go_fmt_command = "gofumpt"
let g:go_imports_mode = "goimports"
```

With `lspconfig` + `gopls`:
```lua
require('lspconfig').gopls.setup({
  settings = {
    gopls = {
      formatting = { gofumpt = true },
    },
  },
})
```

## CI integration

A typical GitHub Actions workflow (`.github/workflows/ci.yml`):

```yaml
name: CI
on: [push, pull_request]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
      - name: tools
        run: |
          go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
          go install mvdan.cc/gofumpt@v0.10.0
          go install golang.org/x/tools/cmd/goimports@v0.28.0
      - run: make check
```

## Troubleshooting

### `golangci-lint: command not found`

Ensure `$(go env GOPATH)/bin` is in your `PATH`, or use:

```sh
$(go env GOPATH)/bin/golangci-lint run ./...
```

### False positives

Suppress a specific issue in code with a comment:

```go
//nolint:errcheck // reason
srv.Start()
```

Suppress by rule in `.golangci.yml` under `issues.exclude-rules`.
