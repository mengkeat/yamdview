# Agent integration guide

How coding agents (Claude Code, Codex CLI, custom scripts, ...) drive yamdview
as a rich human-in-the-loop review surface. This documents the **Phase 14
surface**; field names and status codes below are verified against the
implementation in [`internal/agentapi`](../internal/agentapi),
[`internal/mcp`](../internal/mcp), [`internal/server`](../internal/server),
and [`internal/feedback`](../internal/feedback).

yamdview offers three integration tiers, from zero-dependency to first-class:

| Tier | Command | Best for |
| --- | --- | --- |
| **CLI review** | `yamdview review ...` | Any agent that can run a shell command. One blocking call, one JSON payload on stdout. No protocol support needed. |
| **HTTP API** | `yamdview serve --api` | Agents managing multiple concurrent sessions or streaming output progressively. |
| **MCP server** | `yamdview mcp` | MCP-capable clients (Claude Code, others) that get typed tools over stdio. |

All tiers share the same review session model: the agent presents Markdown
(rendered with KaTeX math, table repair, and the Paper & Ink theme), the human
highlights spans and attaches typed comments, and the agent receives a
versioned feedback payload.

## Tier 1: CLI review (zero-dependency)

`yamdview review` is a blocking CLI contract: present a document, wait for the
human, receive exactly one feedback payload on stdout (all logs go to stderr).

```sh
yamdview review --title "Refactor plan" \
  --prompt "Please review this plan. Highlight anything wrong." \
  --choices "Approve,Request changes" \
  --timeout 15m plan.md
```

Key flags (see `internal/cli/cli.go`):

| Flag | Meaning |
| --- | --- |
| `--title <text>` | Session title shown in the viewer header |
| `--prompt <text>` | The agent's question, shown as a banner above the document |
| `--choices <a,b,c>` | Quick-verdict buttons in the browser |
| `--format json\|markdown` | Feedback payload format (default `json`) |
| `--output <path\|->` | Where to write the payload (default stdout) |
| `--timeout <duration>` | Auto-exit with a timeout verdict |
| `-` (file argument) | Read the document from stdin |

Stable exit codes: `0` submitted, `2` timeout, `3` cancelled, `4` internal
error (no payload guaranteed). The payload schema is shown under
[The feedback payload](#the-feedback-payload) below.

### Claude Code: Bash tool

Tell the agent (e.g. in `CLAUDE.md`):

```markdown
- When presenting a document or plan for my review, use the Bash tool to run:
  yamdview review --title "<short title>" --prompt "<what I should check>" \
    --choices "Approve,Request changes" --timeout 15m <file.md>
  Parse the JSON feedback from stdout. Exit codes: 0 submitted, 2 timeout,
  3 cancelled, 4 internal error. stdout carries only the JSON payload.
```

### Claude Code: slash command

Save as `.claude/commands/review.md` (also provided at
[`examples/claude-code/review.md`](../examples/claude-code/review.md)):

````markdown
---
description: Present $ARGUMENTS for human review
---
Run `yamdview review --title "$ARGUMENTS" --prompt "Please review this document. Highlight anything wrong or unclear, add comments, then choose a verdict and submit." --choices "Approve,Request changes" --timeout 15m <file>` and parse the JSON feedback from stdout. Exit codes: 0 submitted, 2 timeout, 3 cancelled, 4 internal error. stdout carries only the JSON payload.
````

## Tier 2: HTTP API (`yamdview serve --api`)

A long-running loopback server for agents that manage multiple sessions or
stream output chunk by chunk.

### Launching and scraping the startup line

```sh
yamdview serve --api --addr 127.0.0.1:0
```

On startup exactly one parseable line goes to **stderr** (a stable contract,
see `internal/app/app.go`):

```
2026/08/25 22:10:52 api: listening on http://127.0.0.1:44751 bearer 836b2c2c077fadf6...
```

Scrape it for the base URL and bearer token:

```sh
read -r API_URL TOKEN <<EOF
$(grep -o 'api: listening on http://[^ ]* bearer [0-9a-f]*' server.log |
  awk '{print $4, $6}')
EOF
```

Port `0` picks a free port. No browser is opened by the server itself; each
session response carries its own viewer URL to hand to the human. Bind
addresses must be loopback unless `--unsafe-bind` is passed (enforced at
parse time in `internal/cli/cli.go`).

### Session lifecycle cookbook

Every `/api/v1` route requires the header `Authorization: Bearer $TOKEN`
(wrong or missing → `401 {"error":"missing or invalid bearer token"}`).
Request bodies are strict JSON: unknown fields and trailing values are
rejected with `400`.

**1. Create a session** (`markdown` is required; `title`, `prompt`, `choices`
optional):

```sh
curl -sS -X POST "$API_URL/api/v1/sessions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"markdown":"# Plan\n\nFirst draft.","title":"Refactor plan","prompt":"Please review","choices":["Approve","Request changes"]}'
```

```json
{"id":"s-1756150252000000000-9f2a1b3c4d5e6f70","url":"http://127.0.0.1:44751/sessions/s-1756150252000000000-9f2a1b3c4d5e6f70/"}
```

`201 Created`. The session starts **streaming**: the viewer page renders, but
annotation mutations are locked (`409 {"error":"document still streaming"}`)
until the stream is marked complete.

**2. Append Markdown** (progressive rendering; diffs are streamed to open
viewer pages over SSE as block-level patches, not full resets):

```sh
curl -sS -X POST "$API_URL/api/v1/sessions/$ID/append" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"markdown":"\n\n## Risks\nNone identified yet."}'
```

```json
{"state":"streaming","ops_applied":2,"reset":false}
```

`reset:false, ops_applied:N` means N incremental block patches were
broadcast; `reset:true` means a full-document fallback (e.g. reference-style
link definitions changed). Passing `"complete":true` marks the stream
finished in the same call. Appends after completion are allowed while the
session is open — existing annotations are re-anchored by quote and marked
`outdated` if their text disappeared.

**3. Mark complete** (unlocks annotations):

```sh
curl -sS -X POST "$API_URL/api/v1/sessions/$ID/complete" \
  -H "Authorization: Bearer $TOKEN"
```

```json
{"state":"complete"}
```

**4. Give the viewer URL to the human.** Open `"$URL"` (the `url` field from
step 1) in any local browser. The page supports highlight-and-comment
annotations, quick verdicts, and submission.

**5. Long-poll feedback** (returns immediately when the session is already
terminal):

```sh
curl -sS "$API_URL/api/v1/sessions/$ID/feedback?wait=60s" \
  -H "Authorization: Bearer $TOKEN"
```

- `200` — the terminal feedback payload (schema below).
- `408 {"error":"feedback not ready; retry with a longer wait"}` — the wait
  expired while the session was still open; retry.
- `404 {"error":"session not found"}` — unknown or deleted session.

`wait` takes a Go duration (`10s`, `1m`, `0` = no wait). An invalid value is
a `400`.

**6. Replace the document** (optional; full swap instead of append):

```sh
curl -sS -X PUT "$API_URL/api/v1/sessions/$ID/document" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"markdown":"# Plan v2\n\nRewritten."}'
```

Returns the same `{"state","ops_applied","reset"}` shape. Revision bumping
and revision-aware re-anchoring arrive in Phase 16; today the snapshot is
replaced and annotations re-anchor by quote.

**7. Delete the session**:

```sh
curl -sS -X DELETE "$API_URL/api/v1/sessions/$ID" -H "Authorization: Bearer $TOKEN" -i
# HTTP/1.1 204 No Content
```

`DELETE` also cancels a still-open session, waking any long-poll waiters.

Other statuses: `409 {"error":"session is no longer open"}` when mutating a
submitted/timed-out/cancelled session; `413` when a body exceeds the 10 MiB
cap. The root path `GET /` answers with a plain-text `yamdview agent api`
liveness line.

### Browser endpoints (what the viewer page uses)

Each session's viewer is mounted at `/sessions/<id>/`; the page talks to
page-relative endpoints under that prefix (`web/annotator.js`). Mutations
require the per-session token in the `X-Yamdview-Token` header
(`internal/server/server.go`, `SessionTokenHeader`) — this is a separate
secret from the API bearer token and is embedded only in the session's own
HTML (`data-session-token` attribute):

```text
GET    /sessions/<id>/                              viewer page
GET    /sessions/<id>/api/session                   session metadata + annotations
POST   /sessions/<id>/api/session/annotations       create annotation (201)
PATCH  /sessions/<id>/api/session/annotations/<aid> edit annotation
DELETE /sessions/<id>/api/session/annotations/<aid> delete annotation (204)
POST   /sessions/<id>/api/session/submit            {"verdict","summary"} → {"state":"submitted"}
GET    /sessions/<id>/events                        SSE stream (patches, session state)
```

When the session was created with `choices`, the submitted `verdict` must
match one of them exactly (a mismatch is `400`); without choices any
non-empty verdict is accepted.

Annotation creation body (strict; server-owned fields like IDs and source
spans are rejected):

```json
{
  "kind": "suggestion",
  "block_id": "block-3-9c2e1a2b",
  "start_line": 7,
  "end_line": 7,
  "quote": "invalidate the cache on every write",
  "prefix": "we should ",
  "suffix": " to keep it",
  "comment": "Too aggressive.",
  "suggested_replacement": "invalidate only the affected cache keys"
}
```

Kinds: `comment`, `suggestion` (requires `suggested_replacement`),
`question`, `concern`, `approval`. `start_line`/`end_line` are 1-based.
Source-span resolution is best effort: a unique quote match records exact
byte offsets; otherwise the annotation degrades to block + quote anchors,
which are still unambiguous for the agent.

The scripted round-trip in [`examples/agent-round-trip.sh`](../examples/agent-round-trip.sh)
drives these endpoints end to end.

### Concurrency notes

One server hosts any number of sessions; each gets its own ID, viewer
handler, and per-session token (`internal/agentapi/manager.go`). Sessions are
independent: deleting one does not affect others, and appends to different
sessions do not contend. Multiple agents can share one server by sharing the
bearer token, or run one server each.

## The feedback payload

The versioned contract emitted by CLI stdout, the long-poll endpoint, and the
MCP await tool alike (`internal/feedback/feedback.go`). JSON is
deterministic and indented:

```json
{
  "yamdview_feedback_version": 1,
  "session_id": "s-1756150252000000000-9f2a1b3c4d5e6f70",
  "title": "Refactor plan",
  "prompt": "Please review",
  "verdict": "Request changes",
  "summary": "Two issues around the cache layer.",
  "comments": [
    {
      "id": "annotation-1f2e3d4c5b6a7f8e",
      "kind": "suggestion",
      "block_id": "block-3-9c2e1a2b",
      "start_line": 7,
      "end_line": 7,
      "quote": "invalidate the cache on every write",
      "prefix": "we should ",
      "suffix": " to keep it",
      "source_span": { "start_byte": 1180, "end_byte": 1216 },
      "comment": "Too aggressive.",
      "suggested_replacement": "invalidate only the affected cache keys",
      "created_at": "2026-08-25T22:12:01Z",
      "updated_at": "2026-08-25T22:12:01Z",
      "status": "active"
    }
  ],
  "timing": { "opened_at": "2026-08-25T22:10:52Z", "submitted_at": "2026-08-25T22:13:53Z", "duration_ms": 181000 }
}
```

Omitted-when-empty fields: `group_id`, `prefix`, `suffix`, `source_span`,
`comment`, `suggested_replacement`, `updated_at`, `status`, and the whole
`reformulated` object (present only when an approved LLM reformulation ran,
with `provider`, `model`, `text`, `approved_by_user`). `status` is
`"outdated"` when the quoted text no longer resolves after a document
update. `--format markdown` renders the same data as deterministic prose.

## Generic JSON tool schemas

Frameworks that define tools as JSON Schema can paste these directly.

### CLI wrapper: `yamdview_review`

Runs `yamdview review ... <file>` (write the `markdown` property to a temp
file first, or pass `-` on stdin) and parses stdout. Exit codes: 0 submitted,
2 timeout, 3 cancelled, 4 internal error.

```json
{
  "name": "yamdview_review",
  "description": "Present a Markdown document to the human for review in their browser (math, tables, annotations) and block until they submit. Returns the versioned feedback payload on stdout: verdict, summary, and comments with exact quotes, line ranges, and optional source byte spans. Exit codes: 0 submitted, 2 timeout, 3 cancelled, 4 internal error.",
  "parameters": {
    "type": "object",
    "properties": {
      "markdown": { "type": "string", "description": "Inline Markdown content (exactly one of markdown or path)" },
      "path": { "type": "string", "description": "Path to a local Markdown file (exactly one of markdown or path)" },
      "title": { "type": "string", "description": "Session title shown in the viewer header" },
      "prompt": { "type": "string", "description": "Question or request shown above the document" },
      "choices": { "type": "array", "items": { "type": "string" }, "description": "Quick verdict choices offered to the reviewer, e.g. [\"Approve\",\"Request changes\"]" },
      "timeout_seconds": { "type": "integer", "description": "Give up waiting after this many seconds; 0 (default) waits forever (yamdview --timeout)" }
    }
  }
}
```

### HTTP-based tool: `yamdview_present`

For agents that already hold a running `serve --api` instance (base URL and
bearer token discovered from the startup stderr line, e.g. via environment
variables `YAMDVIEW_URL` / `YAMDVIEW_TOKEN`). This tool creates a session,
marks it complete, long-polls once, and returns the payload or a
not-ready signal.

```json
{
  "name": "yamdview_present",
  "description": "Present a Markdown document for human review on the running yamdview API server (YAMDVIEW_URL/YAMDVIEW_TOKEN) and wait for feedback. Marks the document complete, so annotations unlock immediately. Returns the versioned feedback payload, or {\"error\":\"feedback not ready\", \"session_id\": ...} when the human has not responded yet — retry with the session_id.",
  "parameters": {
    "type": "object",
    "properties": {
      "markdown": { "type": "string", "description": "Inline Markdown content" },
      "title": { "type": "string", "description": "Session title shown in the viewer header" },
      "prompt": { "type": "string", "description": "Question or request shown above the document" },
      "choices": { "type": "array", "items": { "type": "string" }, "description": "Quick verdict choices offered to the reviewer" },
      "wait_seconds": { "type": "integer", "description": "How long to long-poll for feedback; 0 returns immediately (default 60)" }
    },
    "required": ["markdown"]
  }
}
```

## Tier 3: MCP server (`yamdview mcp`)

`yamdview mcp` speaks the Model Context Protocol over stdio: newline-delimited
JSON-RPC 2.0, protocol version `2024-11-05`, with the minimal method set
`initialize`, `ping`, `tools/list`, `tools/call` (`internal/mcp/jsonrpc.go`;
a hand-rolled stdlib subset — see the decision note below). stdout carries
protocol messages only; logs go to stderr. The server also starts an
in-process `serve --api`-equivalent listener on `127.0.0.1:0` to host the
per-session viewer pages; its URL and bearer token are logged to stderr
(`mcp: session api on http://127.0.0.1:PORT bearer <token>`) as an optional
HTTP escape hatch.

### Claude Code

```sh
claude mcp add yamdview -- yamdview mcp
```

Or as a project-level `.mcp.json`:

```json
{
  "mcpServers": {
    "yamdview": {
      "command": "yamdview",
      "args": ["mcp"]
    }
  }
}
```

The same `mcpServers` shape works for other MCP clients; pass
`--addr 127.0.0.1:<port>` in `args` if you need a fixed viewer port.

### Tool list

Schemas below are copied from `internal/mcp/tools.go` (the `tools/list`
response). `present_markdown` and `request_review` validate the
markdown/path union semantically (exactly one must be provided), so neither
field is JSON-Schema-required.

**`present_markdown`** — create a review session:

```json
{
  "name": "present_markdown",
  "description": "Present a Markdown document for human review in a browser session; returns {session_id, url, state} as JSON. The session starts in streaming mode with annotations locked; pass complete=true when the document is finished, or complete it later via await_feedback/request_review.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "markdown": { "type": "string", "description": "Inline Markdown content (exactly one of markdown or path is required)" },
      "path": { "type": "string", "description": "Path to a local Markdown file (exactly one of markdown or path is required)" },
      "title": { "type": "string", "description": "Session title shown in the review viewer" },
      "prompt": { "type": "string", "description": "Question or request shown above the document" },
      "choices": { "type": "array", "items": { "type": "string" }, "description": "Quick verdict choices offered to the reviewer" },
      "complete": { "type": "boolean", "description": "Mark the document complete immediately, unlocking annotations (default false)" },
      "open": { "type": "boolean", "description": "Open the viewer in the user's browser (default true)" }
    }
  }
}
```

Result text: `{"session_id":"s-...","url":"http://127.0.0.1:PORT/sessions/s-.../","state":"streaming"}`.
By default the viewer opens in the user's browser; pass `open:false` in
headless contexts.

**`await_feedback`** — wait for the human:

```json
{
  "name": "await_feedback",
  "description": "Wait for the human to submit feedback on a review session and return the versioned feedback payload as JSON. Optionally marks a still-streaming document complete first. With timeout_seconds=0 (default) it waits indefinitely; on timeout the session is still open, so call it again.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "session_id": { "type": "string", "description": "Session ID returned by present_markdown" },
      "timeout_seconds": { "type": "integer", "description": "How long to wait for feedback; 0 (default) waits forever" },
      "complete": { "type": "boolean", "description": "Mark a still-streaming document complete before waiting (default false)" }
    },
    "required": ["session_id"]
  }
}
```

Result text: the feedback payload JSON. On timeout the result is a tool error
(`isError: true`) reading `feedback not ready: session <id> is still open;
call await_feedback (or request_review) again to keep waiting` — the session
remains open, so retrying is safe.

**`request_review`** — one-shot convenience:

```json
{
  "name": "request_review",
  "description": "One-shot review convenience: present a Markdown document (always marked complete), open it in the browser, wait for the human's feedback, and return the versioned feedback payload as JSON.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "markdown": { "type": "string", "description": "Inline Markdown content (exactly one of markdown or path is required)" },
      "path": { "type": "string", "description": "Path to a local Markdown file (exactly one of markdown or path is required)" },
      "title": { "type": "string", "description": "Session title shown in the review viewer" },
      "prompt": { "type": "string", "description": "Question or request shown above the document" },
      "choices": { "type": "array", "items": { "type": "string" }, "description": "Quick verdict choices offered to the reviewer" },
      "open": { "type": "boolean", "description": "Open the viewer in the user's browser (default true)" },
      "timeout_seconds": { "type": "integer", "description": "How long to wait for feedback; 0 (default) waits forever" }
    }
  }
}
```

Protocol notes: unknown tools or invalid arguments are JSON-RPC `-32602`
errors; tool-level failures (unreadable path, unknown session, timeout) are
`isError` results, not protocol errors. Notifications (e.g.
`notifications/initialized`) are acknowledged silently.

### Why a hand-rolled JSON-RPC subset

Recorded decision: the required MCP-over-stdio surface (initialize,
notifications, ping, tools/list, tools/call) is small, and the project's
dependency policy favors stdlib-only implementations over vendoring the
official `modelcontextprotocol/go-sdk` (PLAN.md §3.3, §22.7). The session
layer is shared with the HTTP API either way.

## Security notes

- **Loopback-only by default.** `serve --api` and `mcp` refuse non-loopback
  `--addr` values at parse time (including the empty-host `:8080` form,
  wildcard addresses, and non-loopback hostnames/IPs). Passing
  `--unsafe-bind` opts out deliberately — the bearer token then travels on
  your network, so use a private, trusted network only.
- **Bearer token.** One 64-hex-char token per server process, minted at
  startup and printed only to the local stderr startup line. It never appears
  in API responses or the served pages. Treat it like a password: do not
  paste it into shared logs.
- **Per-session browser token.** Viewer mutations (annotations, submit) use
  a separate per-session secret embedded in that session's HTML
  (`data-session-token`) and sent as `X-Yamdview-Token`. It is never accepted
  in JSON bodies, so a malicious page cannot trigger cross-site submissions
  from plain form posts.
- **Strict decoding.** All request bodies are strict JSON (unknown fields
  rejected) and size-capped (10 MiB API, 1 MiB annotations).
- **Terminal locking.** Once a session is submitted, timed out, or cancelled,
  mutations return `409`; annotations additionally stay locked (`409`)
  while the document is still streaming.
