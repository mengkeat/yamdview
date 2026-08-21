# Phase 9 leftovers: scroll preservation, update highlighting, benchmarks, gated E2E

Date: 2026-08-21
Status: Complete
Affected area: `web/viewer.js`, `web/viewer.css`, `internal/document`, `internal/e2e`

## Summary

Phase 9 ("UX, performance, and polish") had four unchecked leftovers after the
performance pass. All four are now complete, one commit each:

| Commit | Deliverable |
| --- | --- |
| `dae7853` | Scroll preservation across block patches and resets (`web/viewer.js`) |
| `9260e85` | Update highlighting for recently patched blocks (`viewer.js`, `viewer.css`) |
| `30d3713` | Committed benchmarks for large documents and single-block edits (`internal/document/document_bench_test.go`) |
| `915fd08` | Gated browser E2E test for scroll preservation (`internal/e2e/`) |

No production Go code changed; the benchmark and E2E commits are test-only,
and the two UX commits touch only the web assets. `go.mod`/`go.sum` are
unchanged throughout.

## 1. Scroll preservation (dae7853)

### Problem

When a patch batch inserts or deletes blocks above the viewport, every
following block shifts vertically and the reader's position jumps. Full
document resets (`replaceDocument`) also dropped scroll position.

### Design: viewport anchor with fingerprint fallback

Before a patch batch is applied, viewer.js captures an anchor:

- **Element**: the first `.md-block` intersecting the viewport.
- **Offset**: its viewport-relative top (`getBoundingClientRect().top`).
- **Fingerprint**: normalized text content of the block.

After the batch succeeds, the anchor is relocated — by element id first,
falling back to fingerprint match among remaining/new blocks — and corrected:

```js
window.scrollBy(0, el.top - anchor.offset);
```

so the anchor block stays at the same visual position.

### Design choices worth recording

- **KaTeX-aware fingerprinting.** KaTeX rewrites `.math[data-tex]` span
  contents in place when rendering, so naive `textContent` fingerprints differ
  before/after math render. `blockFingerprint()` clones the block and collapses
  each math span back to its raw `data-tex` attribute before normalizing
  whitespace. This keeps fingerprints stable across the render step that runs
  right after patch application.
- **Safety rules over cleverness.**
  - Anchor deleted and no fingerprint match → scroll left untouched (never
    guess a position).
  - Reader at the very top of the page → stays pinned to the top.
  - Delta of zero → no `scrollBy` call at all.
- **Failure path untouched.** If any op in the batch fails, `applyPatches`
  still returns `false` and the existing `refreshFromSnapshot` fallback runs;
  anchor capture/restore only wraps fully successful batches.
- **Reset handling.** `replaceDocument` records `pageYOffset`/`scrollTop`
  before the swap and restores it immediately after the `innerHTML` swap inside
  the nested double-`requestAnimationFrame`, covering both SSE `reset` events
  and snapshot refreshes.

## 2. Update highlighting (9260e85)

### Design: pseudo-element overlay + theme token

- New `--update-glow` CSS custom property:
  - Light ("Paper & Ink"): `rgba(199, 81, 59, 0.16)`
  - Dark ("Espresso & Gold"): `rgba(212, 132, 107, 0.20)`
  A slightly stronger sibling of the existing `--accent-glow`, so the tint
  reads on both paper tones without introducing a foreign color.
- `markBlockUpdated(el)` adds `md-block--updated`; CSS renders the highlight as
  an absolutely-positioned `::before` overlay (`inset: -0.2em -0.4em`,
  rounded, `pointer-events: none`) animated from opacity 1 → 0 over 1.8s via
  `@keyframes md-block-fade`.

### Design choices worth recording

- **Overlay instead of background** so block-specific backgrounds (code paper,
  blockquote glow) are never overridden. No conflict with `pre::before`
  because blocks are `<section>` wrappers and the strip lives on the inner
  `<pre>`.
- **Removal is doubly guarded**: `animationend` listener filtered by
  `animationName === "md-block-fade"` (descendant animations bubbling up can't
  remove the class early) plus a 2200 ms timeout fallback.
- **`prefers-reduced-motion: reduce`** skips the class entirely in JS and sets
  `animation: none; opacity: 0` in CSS — no flash either way.
- **Resets stay calm.** Only `replace` / `insert_after` / `insert_before`
  targets are highlighted; a full reset highlights nothing (the existing fade
  transition is enough).
- **Interaction with scroll anchoring verified safe**: `blockFingerprint()`
  reads only text content, so the transient class attribute cannot affect
  anchor matching.

## 3. Committed benchmarks (30d3713)

`internal/document/document_bench_test.go` adds three benchmark families ×
1/5/10 MB synthetic documents:

- `BenchmarkBuildSnapshotFresh/{1,5,10}MB`
- `BenchmarkBuildSnapshotSingleBlockEdit/{1,5,10}MB` (previous snapshot reused)
- `BenchmarkDiffSingleBlockEdit/{1,5,10}MB` (patch-op generation)

### Generator design

- Deterministic: 120 repeated sections × (heading + long paragraph with inline
  math `$a_j + b_j = n$` + valid pipe table + closing paragraph) = 481 blocks.
- Sized to stay **under the `maxDiffMatrixCells` (250k) LCS cutoff**, so `Diff`
  produces real incremental ops rather than degrading to a full reset — this is
  asserted in-benchmark with `b.Fatal` guards.
- Single edit swaps one word in the paragraph nearest the document middle.
- Setup outside the timed loop; all benchmarks use `b.ReportAllocs()` +
  `b.ResetTimer()`.

### Measured numbers (AMD Ryzen 7 7700)

| Benchmark | 1 MB | 5 MB | 10 MB |
| --- | --- | --- | --- |
| Fresh snapshot | 69 ms / 46 MB allocs | 275 ms / 211 MB | 534 ms / 432 MB |
| Single-block edit | 14 ms / 13 MB | 63 ms / 62 MB | 141 ms / 129 MB |
| Diff patch ops | 3.9 ms / 11 MB | 6.7 ms / 46 MB | 10.7 ms / 97 MB |

Confirms the ~4–5× incremental win claimed during the earlier ad hoc
measurement. Noted for future optimization: incremental builds still scale with
document size because block reuse re-segments the whole document and re-renders
the full block-wrapped HTML (`RenderBlocks`). The plan's target (< 100 ms
single-paragraph edit) holds through ~5 MB; 10 MB is at 141 ms.

Run with:

```sh
make test env + go test -bench=. -run='^$' ./internal/document
```

## 4. Gated E2E scroll-preservation test (915fd08)

Implements the plan §16.3 contract:
`YAMDVIEW_E2E=1 go test ./internal/e2e`.

### Structure

- `internal/e2e/e2e_test.go` — Go test: starts the real viewer server on
  loopback with a generated 120-paragraph document, locates node + Chrome, execs
  the driver, and drives the scenario over a small JSON-lines protocol
  (`ready` → parent rewrites file + broadcasts patches + writes `go` to driver
  stdin → `done`).
- `internal/e2e/driver.js` — Node script using **puppeteer-core**, resolved via
  `NODE_PATH` so no `package.json`, no npm install, and **zero new Go module
  dependencies** (`go.mod` unchanged).

### Scenario

1. Load page headless (900×700), wait for ≥121 `.md-block`s.
2. Scroll to the middle of the document (top-of-page is anchored by design, so
   the interesting case requires being scrolled away).
3. Tag a fully visible reference block with a JS expando property.
4. Parent prepends 8 new paragraphs to the temp file, rebuilds the snapshot,
   diffs, and broadcasts the SSE patches.
5. Driver polls until the inserted blocks appear, then measures.

### Assertions

- **Scroll preserved**: reference block's viewport-relative top within ±2px of
  its pre-patch value.
- **No full reset**: the tagged pre-edit DOM node must still be reachable with
  its expando intact — a reset replaces `#document`'s innerHTML, which destroys
  every original node. Console warnings containing "falling back to snapshot
  reset" are treated as failures too.
- Block count grows by exactly the number of inserted paragraphs.

### Gating semantics

- Skips unless `YAMDVIEW_E2E=1`.
- With the flag set but no browser/node/puppeteer-core found → `t.Skip` with a
  pointer to the override env vars (`YAMDVIEW_E2E_BROWSER`,
  `YAMDVIEW_E2E_NODE_PATH`), never a failure.
- Plain `go test ./...` / `make test` stays fast and hermetic.

### Environment notes

- Browser discovery order: `YAMDVIEW_E2E_BROWSER` → glob
  `~/.cache/ms-playwright/chromium-*/chrome-linux*/chrome` → common system
  Chrome/Chromium paths.
- Node discovery: `PATH` → `~/.nvm/versions/node/*/bin/node`.
- Chrome launched with `--headless=new --no-sandbox --disable-gpu
  --disable-dev-shm-usage`.
- Verified passing locally (~4 s); `make test` unaffected.

## Process notes

- Each subtask was delegated to a subagent and committed separately to keep
  the history modular; the E2E subagent stalled once and was re-run, with the
  final commit made after verifying the passing run directly.
- `PLAN.md` intentionally not modified per repo policy — it remains a
  local-only planning artifact. The four Phase 9 boxes there are now done in
  code even though the checklist itself is unchanged.
- Pre-existing golangci-lint findings elsewhere in the tree were left alone;
  neither new file introduces lint findings.
