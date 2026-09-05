# Architecture specification

**Status:** Proposed  
**Last reviewed:** 2026-09-05  
**Scope:** Source-aware Markdown rendering and trustworthy human feedback for
CLI, HTTP, and MCP coding-agent integrations.

## 1. Purpose

YAMDView should produce the same trustworthy document and review semantics
regardless of whether content comes from a file, stdin, the HTTP API, or MCP.
Two results are authoritative:

1. **Rendered document:** original source, source identity, resolved rendering
   policy, parsed structure, transformations, mappings, and diagnostics travel
   together. HTML is an output, not the document model.
2. **Review result:** the review session owns document identity, annotations,
   lifecycle, and the immutable submitted feedback. CLI, HTTP, MCP, and browser
   code are adapters to the same operations.

The existing technology choices remain appropriate: Go, Goldmark, vendored
KaTeX, vanilla browser JavaScript, and validated atomic file writes. This
specification does not propose a generic plugin pipeline, database, event
store, agent-specific engines, or frontend rewrite.

## 2. Goals and non-goals

### Goals

- Render explicit TeX and supported Unicode equations predictably.
- Preserve source meaning when automatic math recognition is uncertain.
- Support equation-like text inside backticks only through an explicit policy.
- Keep original UTF-8 source coordinates through deterministic repairs.
- Use one rendering contract for export, live view, review, HTTP, and MCP.
- Bind annotations and final feedback to the exact document reviewed.
- Prevent unsaved comments, stale browser state, or late LLM responses from
  silently changing submitted feedback.
- Give coding agents stable, versioned feedback they can verify before editing.
- Preserve offline-first behavior and source files by default.

### Non-goals

- Perfect interpretation of arbitrary mathematical Unicode.
- Character-perfect source maps for every generated DOM node.
- Automatic execution of reviewer instructions or source patches.
- Public multi-user hosting or identity management.
- Persistent conversational-agent infrastructure.
- Rich diagrams, syntax highlighting, image handling, or session persistence
  before rendering and review correctness are established.

## 3. Evidence motivating the design

The reported failure was reproduced against
`research-log/2026-09-04-space-solar-conclusion.md` in the external
`solar-panel` repository:

```sh
make build
./bin/yamdview --export /tmp/yamdview-space-solar-baseline.html \
  /path/to/solar-panel/research-log/2026-09-04-space-solar-conclusion.md
```

The export contained 114 math placeholders. Decoding their `data-tex` values
and validating them with the vendored KaTeX `renderToString` implementation
found one parse failure. This validates generated TeX syntax, not visual
browser layout.

| Source form | Observed result | Cause |
| --- | --- | --- |
| `` `n = √(μ/a³)` `` | Preserved as code | Backticks deliberately exclude content from Unicode conversion. |
| Backticked thermal, PV, and beam expressions | Preserved as code | Same policy; at least 16 math-bearing code spans occurred in this document. |
| `(DoD · η_discharge)` in prose | Generated `\cdot \eta_`, leaving `discharge` as prose | Recognition accepts `_` but stops before the multi-letter subscript. KaTeX rejects the dangling underscore. |
| Backticked optics and dynamics expressions containing `ω̇` | Preserved as code | Supporting these requires complete identifiers and combining accents, not only character substitution. |

Two distinct cases therefore need different behavior:

- A malformed automatic conversion is a correctness defect.
- A formula inside backticks is literal code under current Markdown semantics.
  Supporting it must be deliberate rather than achieved by disabling code
  protection globally.

## 4. Design principles

1. **Preserve first.** If a complete expression cannot be recognized and
   validated, retain the original text and report why.
2. **Parse before deciding context.** Goldmark's full-document AST determines
   whether text is prose, code, a table, a list, a reference, or explicit math.
3. **Transform once.** Rendering records every deterministic edit and
   diagnostic. Persistence consumes that result rather than rediscovering it.
4. **Original coordinates are canonical.** Byte and line ranges refer to the
   original UTF-8 source. Browser UTF-16 offsets are translated explicitly.
5. **Identity crosses every boundary.** Browser events, mutations, and feedback
   identify the source revision they describe.
6. **Raw feedback is authoritative.** LLM reformulation is optional enrichment.
7. **Correct full reset beats incorrect incremental rendering.** Optimize only
   after the full-document semantics are covered by tests and benchmarks.
8. **Capabilities, not brands.** Agent adapters are selected by protocol needs,
   not separate code paths for individual coding-agent products.

## 5. Rendering architecture

### 5.1 Pipeline

```text
file / stdin / agent Markdown + resolved render options
    -> immutable original bytes + SHA-256 identity
    -> bounded malformed-table recovery with recorded source edits
    -> Goldmark full-document parse
    -> context-aware math recognition on eligible AST nodes
    -> render result: HTML + blocks + mappings + repairs + diagnostics
    -> document snapshot and stable-ID diff
    -> export | live viewer | review

deterministic source edits -> validated atomic fixer, only when requested
optional LLM suggestions -> revision-bound render overlay, never source writes
```

All entry points call this pipeline with the same resolved options. A render
policy is part of render identity, so cached output cannot be reused across
incompatible policies.

### 5.2 Render result

The concrete render result contains only data required by callers:

```text
RenderResult
  source_sha256
  render_policy
  html
  blocks[]
    id
    original UTF-8 byte range and line range
    html
    selectable source projection
    math nodes[]
      original notation and source range
      converted TeX
      inline/display mode
  deterministic edits[]
    original range and expected old text
    replacement
    reason
    write eligibility
  diagnostics[]
    stable code, stage, severity, and original range
```

Document identity and DOM block identity are separate. Source changes advance
revision even when generated HTML is unchanged.

Unchanged text maps exactly. Transformed math is one selectable unit mapped to
its complete original expression. Generated separators and diagnostic badges
have no editable source. Unsupported selections return an explicit unsupported
or ambiguous result instead of a guessed range. KaTeX's alternate accessibility
tree remains available to assistive technology but is excluded from the
selection projection to avoid duplicate text.

### 5.3 Package responsibilities

| Package | Responsibility |
| --- | --- |
| `internal/markdown` | Full-document parsing, source-aware rendering, and shared render-result types. |
| `internal/mathfix`, `internal/mathchars` | Pure candidate recognition, token conversion, and character tables. No file IO, browser logic, providers, or independent Markdown parsing. |
| `internal/tablefix` | Narrow recovery of malformed tables before parsing, with recorded edits. |
| `internal/document` | Source/render identity, snapshots, stable block IDs, diff, and policy-aware reuse. It consumes renderer results instead of deciding Markdown syntax independently. |
| `internal/fixer` | Validation and optional atomic application of recorded deterministic edits, retaining backup behavior. |
| `internal/llm`, `internal/llmapp` | Provider IO, validation, and optional revision-bound render overlays. |
| `internal/annotation` | Resolution and reanchoring through recorded mappings with explicit unique, ambiguous, outdated, and unsupported outcomes. |

Do not add a generic `core` package. Shared types belong to the package that
produces and defines their semantics.

### 5.4 Full-document semantics and incremental updates

Parse the whole document before choosing patch boundaries. This preserves
references, footnotes, nested lists, blockquotes, and heading context. A
context-sensitive change may trigger a full reset, but reset output must still
contain anchorable block wrappers and mappings.

Block reuse is permitted only when source, render policy, and relevant document
context match. Existing benchmarks should establish whether additional reuse
logic is worthwhile. Optional LLM output is a separate overlay and may not be
cached as though it were deterministic source rendering.

### 5.5 Math recognition policy

1. Explicit `$…$`, `\(…\)`, display delimiters, and `math` fences have priority.
   Protection is span-based, not a whole-paragraph substring test. Escaped
   delimiters and currency have independent negative cases.
2. Unicode recognition tokenizes complete identifiers and subscripts, numbers,
   operators, grouping, known functions, units, and scientific notation.
   Candidate conversion is atomic: it either produces valid complete TeX or
   preserves the original candidate with a diagnostic. A dangling `_` or `^`
   must never be emitted.
3. Layout follows AST context rather than expression-to-paragraph length ratio.
4. Named subscripts such as `η_discharge`, Greek and ASCII bases, script runs,
   grouped roots, and supported combining accents such as `ω̇` are handled as
   complete tokens.
5. Unicode normalization is never applied to the whole document. Recognition
   must not invent limits, grouping, variables, units, or physical meaning.
6. LLM output is optional advice for unresolved candidates, not the primary
   recognizer or authority on mathematical meaning.

Code remains literal by default. One explicit compatibility option is proposed:

```text
--math-in-code=off|equations
```

`off` is the default. `equations` inspects inline-code AST nodes for complete,
formula-like expressions. It does not reinterpret programming fences, paths,
configuration assignments, or arbitrary identifiers. Recognized expressions
render without becoming write-fix candidates and retain a source/copy
affordance. Ambiguous content remains code with a reason and an explicit-TeX
suggestion.

The existing conversion of short `text` and `txt` fences that resemble display
equations remains a documented compatibility exception initially. It must be
covered by the same positive and negative corpus before it is broadened.

### 5.6 Diagnostics and inspection

An optional sidecar is proposed for offline inspection:

```text
--diagnostics-json PATH
```

The manifest includes source digest, render policy, conversions, preserved-code
candidates, unresolved expressions, and renderer failures. A successful export
must not imply that every equation rendered.

Generated TeX is checked against the vendored KaTeX implementation in
development tests; this does not add a JavaScript runtime to the shipped binary.
At runtime, browser KaTeX errors carry math-node and document revision identity.
Offline exports retain readable source fallback and local error details because
no server exists to receive client error reports.

## 6. Review architecture

### 6.1 Ownership

The session is the authority for source, snapshot, revision, annotations,
lifecycle, and final feedback. The review manager owns session lookup, resource
lifetime, and serialized document-update orchestration. HTTP handlers, CLI, MCP,
and browser code adapt the same service operations.

When all transports are migrated, the existing manager should move from
HTTP-specific ownership into a concrete `internal/review` service rather than
being duplicated. Interfaces are introduced only where there are multiple real
implementations, such as provider IO.

Rendering occurs outside the session lock. Publication is atomic and succeeds
only if the expected revision is still current. Initially, one document update
at a time per session is sufficient.

### 6.2 Identity

Each review exposes:

- `document_revision`: monotonic and advanced whenever source bytes change.
- `source_sha256`: identity of the exact original bytes.
- `render_policy`: resolved rendering behavior used for those bytes.
- `review_version`: monotonic identity for reviewer-visible state, including
  document publication, annotations, visible overlays, and lifecycle changes.

Bootstrap data, snapshots, events, mutation responses, client error reports,
and final feedback carry these values. Mutations declare the expected identity.
Stale requests return a conflict plus current metadata; they are never silently
rebased.

### 6.3 Lifecycle

Retain the existing terminal states: submitted, timed out, and cancelled. An
open review distinguishes:

- **streaming:** source may change and review controls are visibly locked.
- **ready:** the published revision is complete and may be annotated.

Replacing a ready document republishes a new revision and invalidates stale
browser drafts and LLM previews. Submitted sessions never reopen. A later agent
iteration creates a new session linked by `previous_session_id`.

Submission validates verdict, expected identity, pending annotation writes, and
optional reformulation preview ID, then freezes annotations and final feedback
under one lock. Repeating the same submission returns the same payload;
conflicting submissions fail without mutation.

Annotation-group creation, update, and deletion are atomic. Every selection
action gets a fresh group ID. PATCH field presence is preserved so an explicit
empty value can clear a field. Reanchoring retains original revision and quote
while reporting a current status of resolved, ambiguous, outdated, or
unsupported.

### 6.4 Browser responsibilities

The browser owns drafts, not authoritative feedback. The annotator exposes one
`flush()` promise. Submit waits for it, remains disabled while saving, and stops
on failure. The UI reports saving, saved, failed, streaming, ready,
disconnected, and stale states. Network failure preserves the local draft.

The viewer consumes lifecycle and revision events. On initial connection,
reconnection, an event gap, or queue overflow, it fetches current metadata and
the current snapshot. It does not assume that a sequence of best-effort patches
converged. A replay log is unnecessary initially.

### 6.5 Feedback contract

Safety-significant additions require a versioned feedback schema. The proposed
shape is:

```text
schema_version
session_id
previous_session_id?
state
verdict
summary
document: {revision, source_sha256, render_policy}
review_version
comments: [{
  id, group_id?, kind, quote, comment, suggested_replacement?,
  original_revision, current_resolution_status,
  source_span?, block_id, line_range
}]
reformulated?: {
  preview_id, input_digest, provider, model, text, approved_by_user
}
```

A legacy CLI output mode may be retained if compatibility requires it. Agents
must verify source digest and exact old text before applying a suggested edit.
A rendered quote is not an executable patch. Unresolved comments remain useful
but carry no falsely exact source span.

Review approval applies to the identified artifact, not arbitrary future agent
commands. YAMDView returns intent; the coding agent remains responsible for
edits, validation, and reporting what changed.

### 6.6 LLM reformulation

Reformulation receives the complete typed review, including verdict, annotation
kind, status, identity, and suggested replacement. A preview has a unique ID and
input digest. Relevant edits invalidate it. Submission approves the exact
preview shown, atomically with finalization.

Late or stale provider results are discarded. Requested model changes must be
allowlisted and applied to the provider's actual request, not only response
metadata. Raw feedback remains authoritative on every provider failure. Auto
mode runs only for submitted feedback, respects cancellation, and cannot mutate
an already published terminal payload.

## 7. Agent integration

Adapters are selected by capability:

| Situation | Integration | Contract |
| --- | --- | --- |
| Shell-capable agent | Blocking `yamdview review` with file or stdin | JSON on stdout, logs on stderr, stable submitted/timeout/cancel outcomes, executable-level tests. |
| MCP-capable client | `present_markdown`, then bounded `await_feedback` | Pending result includes session ID and URL; retries await the same session; ping and cancellation remain responsive. |
| Long-running or concurrent agents | HTTP API | Independent sessions, bounded polling, expected-revision append/replace, explicit complete and cancel operations. |
| Streaming generation | HTTP append; MCP update only when demonstrated necessary | Incomplete syntax may remain literal; review stays locked until complete; source and session sizes are bounded. |
| Remote browser over SSH | Loopback listener plus explicit SSH tunnel | Never default to a public bind for convenience; show the usable viewer URL. |
| Headless rendering/debugging | Offline export and diagnostics manifest | No browser or provider call; same render policy as live and review modes. |

Domain outcomes are normalized as `pending`, `submitted`, `cancelled`,
`expired`, `conflict`, and `not_found`, while transport semantics remain clear.
A wait timeout does not necessarily expire a review. Cancelling an MCP wait
cancels that request, not unrelated sessions. A CLI timeout may end the one-shot
session owned by that process.

MCP must keep its input loop responsive, use per-request contexts, serialize
response writes, bound messages before allocation, and cancel outstanding work
on EOF. A timed-out `request_review` must direct the client to
`await_feedback` with the existing session ID rather than create a duplicate.
The official Go MCP SDK should replace the handwritten subset only if it removes
more protocol and lifecycle code than it adds, as shown by contract tests.

A normal iteration is:

```text
agent presents artifact and identity
    -> human reviews
    -> agent receives versioned raw feedback
    -> agent verifies source, edits, and tests
    -> agent presents a linked review describing what was or was not addressed
```

No persistent agent-conversation framework is required for this loop.

## 8. Security and resource boundaries

- Bind review, HTTP API, and MCP-hosted viewers to loopback by default.
  Non-loopback access requires one consistent explicit unsafe option.
- Treat viewer URLs as sensitive capabilities. A known viewer URL currently
  exposes HTML containing its mutation token; API bearer authentication alone
  is not multi-user viewer authentication.
- Validate Host and Origin where applicable and send restrictive framing and
  referrer policies.
- Bound HTTP bodies, MCP messages, inline Markdown, total sessions, and document
  size before unbounded allocation.
- Path inputs must be regular files and remain subject to the documented local
  trust boundary.
- Validate JSON-RPC version, request identifiers, methods, and cancellation.
- Preserve Goldmark's safe HTML defaults, token comparison, opt-in file writes,
  provider deadlines, and raw-feedback fallback.
- Test DNS-rebinding and shutdown-race hypotheses before introducing broader
  infrastructure specifically for them.

## 9. Delivery plan

Correctness fixes and architectural migration should be separate, reviewable
changes. Each stage has an acceptance gate.

### Stage A: restore the basic journey

- Add small repository fixtures derived from the reported equations plus
  negative controls for paths, commands, source code, and currency. Do not copy
  or depend on the external document.
- Fix complete named-subscript recognition and reject malformed TeX candidates.
- Fix browser selection, shared default verdict choices, executable respond
  configuration, and annotation flush-before-submit.
- Add a real selection-to-comment-to-immediate-submit browser regression,
  API/MCP omitted-choice checks, and executable mock-provider smoke test.
- Validate every generated fixture expression with vendored KaTeX.

**Gate:** all emitted fixture TeX parses, negative controls remain unchanged,
and immediate submission includes the latest comment. Backticked formulas are
still expected to remain code until Stage B.

### Stage B: establish source-aware rendering

- Introduce `RenderResult` and record deterministic edits, original ranges, and
  diagnostics once.
- Migrate snapshots and fixer to consume recorded results.
- Use full-document Goldmark context for block and math decisions while keeping
  narrow malformed-table recovery.
- Implement complete math tokens and `--math-in-code` policy.
- Map transformed equations atomically to original source and preserve
  anchorable reset output.
- Add the diagnostics manifest and route unresolved candidates to optional LLM
  repair.

**Gate:** the solar fixture renders under `--math-in-code=equations`; default
literal code, explicit TeX, prose, currency, references, nested lists, and
write-fix safety do not regress.

### Stage C: make review identity trustworthy

- Implement source digest, document revision, and review version.
- Require expected identity for document, annotation, and submit mutations.
- Centralize verdict defaults and validation; make group mutations atomic.
- Freeze terminal feedback atomically and distinguish reanchor outcomes.
- Add revision-aware browser state and snapshot reconciliation.
- Publish the versioned feedback contract with compatibility tests.

**Gate:** replacing a document during review creates a visible conflict instead
of misattached feedback; reconnect converges; terminal payloads cannot change;
math and Unicode selections map correctly or report explicit limitations.

### Stage D: unify orchestration and harden transports

- Move the existing manager into the shared review service and route CLI, HTTP,
  and MCP through it.
- Resolve rendering options once and use them for every initial render and
  update.
- Make MCP waits concurrent and cancellation-aware; return structured pending
  results and enforce input limits.
- Apply consistent local-capability security and shutdown/resource limits.
- Add retry safety where client request IDs are justified.

**Gate:** the same fixture and policy produce equivalent content through export,
live view, CLI review, HTTP, and MCP; waits do not block control traffic;
cancellation is isolated; shutdown leaves no active work.

### Stage E: bind optional LLM feedback and automate checks

- Preserve typed reviewer intent and bind approval to exact preview identity.
- Reject stale provider output and honor real model override and cancellation.
- Keep provider overlays separate from deterministic rendering and source edits.
- Replace machine-specific browser dependency discovery with pinned, documented
  development tooling and Makefile targets.
- Maintain MCP transcript, feedback-schema, and documentation fixtures.

**Gate:** preview A cannot approve preview B; late replies cannot alter submitted
feedback; provider output is never written to source; required release checks
exercise the real executable and browser rather than silently skip them.

Only then should the project reconsider rich diagrams, highlighting, image
handling, restart persistence, and session browsing. Those features must consume
these rendering, mapping, diagnostic, and review contracts.

## 10. Validation

Use the existing test layout:

- **Go tests:** token boundaries, delimiters and currency, table edits, source
  mapping, mutation identity, anchoring ambiguity, terminal immutability, and
  transport schemas.
- **Corpus tests:** explicit TeX, Unicode inline/display math, backticked
  formulas, code fences, named subscripts, combining marks, tables, nested
  structures, references, repeated text, and partial streamed content. Assert
  intended preservation as well as conversion.
- **KaTeX tests:** export fixtures, decode actual placeholders, and parse every
  generated expression with the shipped KaTeX.
- **Browser tests:** use offline export for layout and math inspection; use a
  live server only for selection, saving, submission, reconnect, multi-tab,
  and keyboard behavior. Fail on unexpected console errors.
- **Contract tests:** exercise the actual CLI process, HTTP API, and MCP
  transcripts for pending/retry/submit, cancellation, EOF during wait, stale
  source, omitted choices, provider failure, and sensitive provider errors.
- **Performance tests:** record current large-document cold-render, reload, and
  memory baselines before replacing segmentation. Prefer a correct full reset
  to speculative context dependency tracking.

`make test` and `make test-race` remain the baseline checks. The gated real
browser suite is a separate required check for browser-facing acceptance gates.

## 11. Completion criteria

This specification is implemented when:

- Every reported formula is either rendered correctly or carries an explicit
  preserved/unresolved reason.
- Source files remain unchanged by default.
- A reviewer can annotate the artifact actually shown in the browser.
- Every returned comment identifies the exact reviewed source and policy.
- Submission cannot lose a pending edit.
- Submitted feedback is immutable.
- Export, live view, CLI review, HTTP, and MCP share rendering and review
  semantics.
- Agents can verify suggested edits against source rather than trust a rendered
  quote as a patch.
