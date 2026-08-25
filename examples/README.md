# Examples

Runnable demonstrations of yamdview's agent integration surfaces. Full
documentation lives in [`docs/agent-integration.md`](../docs/agent-integration.md).

## `agent-round-trip.sh`

A portable bash script that scripts the complete HTTP API review loop against
`yamdview serve --api`: it launches the server on a random loopback port,
scrapes the `api: listening on ... bearer ...` stderr startup line, creates a
streaming session, appends Markdown (demonstrating progressive block patches),
marks the document complete, then plays the human reviewer — pulling the
per-session token from the viewer page HTML, posting one paragraph annotation,
and submitting an "Approve" verdict — before long-polling the feedback
endpoint, pretty-printing the versioned payload, and deleting the session.

```sh
make build
./examples/agent-round-trip.sh                     # uses ./bin/yamdview and a generated sample document
./examples/agent-round-trip.sh ./bin/yamdview doc.md   # explicit binary and document
```

Requires `curl` and `python3` or `jq`. No browser is involved; every mutation
is driven over HTTP exactly as an agent would.

## `claude-code/review.md`

A Claude Code slash command (drop into `.claude/commands/review.md`) that
presents a file for human review via `yamdview review` and parses the JSON
feedback from stdout. Demonstrates the zero-dependency CLI tier.
