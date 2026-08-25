#!/usr/bin/env bash
# agent-round-trip.sh - scripted agent round trip against `yamdview serve --api`.
#
# Usage:
#   ./examples/agent-round-trip.sh [path/to/yamdview] [sample.md]
#
# Environment:
#   YAMDVIEW_BIN   path to the yamdview binary (default: ./bin/yamdview;
#                  override with the first argument)
#
# Requirements: curl, and python3 or jq for JSON handling.
#
# This script builds nothing itself; run `make build` first or pass the
# binary path. It demonstrates the full HTTP API lifecycle documented in
# docs/agent-integration.md ("HTTP API tier"):
#
#   1. launch `yamdview serve --api` on a random loopback port and scrape
#      the `api: listening on http://... bearer <token>` stderr startup line
#   2. create a streaming review session (title, prompt, verdict choices)
#   3. append more Markdown (progressive block patches), mark it complete
#   4. play the human: pull the per-session token out of the viewer page
#      HTML, post one paragraph annotation, submit an "Approve" verdict
#   5. long-poll the feedback endpoint and pretty-print the payload
#   6. delete the session and shut the server down cleanly

set -euo pipefail

progname=${0##*/}

die() {
	printf '%s: %s\n' "$progname" "$*" >&2
	exit 1
}

step() {
	printf '\n== %s\n' "$1"
}

BIN=${1:-${YAMDVIEW_BIN:-./bin/yamdview}}
[ -x "$BIN" ] || die "yamdview binary not found or not executable: $BIN
(build it with 'make build', pass its path as the first argument, or set YAMDVIEW_BIN)"

# --- JSON helpers: prefer python3, fall back to jq, otherwise bail. --------
if command -v python3 >/dev/null 2>&1; then
	json_str() { python3 -c 'import json,sys; sys.stdout.write(json.dumps(sys.stdin.read()))'; }
	json_field() {
		python3 -c 'import json,sys
d = json.load(sys.stdin)
v = d[sys.argv[1]]
print(json.dumps(v) if isinstance(v, (bool, dict, list)) else v)' "$1"
	}
	json_pretty() { python3 -m json.tool; }
elif command -v jq >/dev/null 2>&1; then
	json_str() { jq -Rs .; }
	json_field() { jq -r --arg f "$1" '.[$f] | if type == "string" then . else tostring end'; }
	json_pretty() { jq .; }
else
	die "neither python3 nor jq is available; one of them is required for JSON handling"
fi

# --- workspace and cleanup --------------------------------------------------
work=$(mktemp -d "${TMPDIR:-/tmp}/yamdview-round-trip.XXXXXX")
server_log=$work/server.log
server_pid=
cleanup() {
	if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

# --- sample document (mixed content: heading, prose, math, table) ----------
doc=${2:-}
if [ -z "$doc" ]; then
	doc=$work/sample.md
	cat >"$doc" <<'EOF'
# Agent round-trip demo

This document exercises the full review loop: the agent streams it to a
browser session, the human highlights a paragraph and comments, and the
structured feedback flows back to the agent as JSON.

Inline math like $E = mc^2$ and Unicode math like ∀x ∈ ℝ, x² ≥ 0 both
render through the vendored KaTeX pipeline without touching the source.

| Tier | Command |
| ---- | ------- |
| CLI | yamdview review |
| HTTP API | yamdview serve --api |
| MCP | yamdview mcp |
EOF
	echo "created sample document: $doc"
else
	[ -f "$doc" ] || die "sample markdown not found: $doc"
fi

# --- 1. launch the API server and scrape the startup line ------------------
step "starting $BIN serve --api"
"$BIN" serve --api --addr 127.0.0.1:0 2>"$server_log" &
server_pid=$!

startup=
for _ in $(seq 1 100); do
	startup=$(grep -o 'api: listening on http://[^ ]* bearer [0-9a-f]*' "$server_log" 2>/dev/null | head -1 || true)
	[ -n "$startup" ] && break
	if ! kill -0 "$server_pid" 2>/dev/null; then
		cat "$server_log" >&2
		die "server exited before printing its startup line"
	fi
	sleep 0.1
done
if [ -z "$startup" ]; then
	cat "$server_log" >&2
	die "timed out waiting for the 'api: listening' startup line"
fi

api_url=$(printf '%s\n' "$startup" | awk '{print $4}')
token=$(printf '%s\n' "$startup" | awk '{print $6}')
echo "api url:  $api_url"
echo "bearer:   ${token%????????????????????????????????????????????????????}..."

# --- 2. create a streaming session ------------------------------------------
step "POST /api/v1/sessions"
create_body=$(
	printf '{"markdown":%s,"title":%s,"prompt":%s,"choices":[%s,%s]}' \
		"$(json_str <"$doc")" \
		"$(printf '%s' 'Agent round-trip demo' | json_str)" \
		"$(printf '%s' 'Please review this document. Highlight anything wrong or unclear, then choose a verdict.' | json_str)" \
		"$(printf '%s' 'Approve' | json_str)" \
		"$(printf '%s' 'Request changes' | json_str)"
)
created=$(curl -sS -X POST "$api_url/api/v1/sessions" \
	-H "Authorization: Bearer $token" \
	-H 'Content-Type: application/json' \
	--data-binary "$create_body") || die "create request failed"
sid=$(printf '%s' "$created" | json_field id)
viewer_url=$(printf '%s' "$created" | json_field url)
[ -n "$sid" ] || die "no session id in create response: $created"
echo "session:  $sid"
echo "viewer:   $viewer_url"

# --- 3. append a section, then mark the stream complete ---------------------
step "POST /api/v1/sessions/\$id/append"
append_chunk=$'\n\n## Risks\n\nNone so far. This section was appended after session creation to demonstrate progressive rendering.'
append_body=$(printf '{"markdown":%s}' "$(printf '%s' "$append_chunk" | json_str)")
appended=$(curl -sS -X POST "$api_url/api/v1/sessions/$sid/append" \
	-H "Authorization: Bearer $token" \
	-H 'Content-Type: application/json' \
	--data-binary "$append_body") || die "append request failed"
echo "append:   state=$(printf '%s' "$appended" | json_field state) ops_applied=$(printf '%s' "$appended" | json_field ops_applied) reset=$(printf '%s' "$appended" | json_field reset)"

step "POST /api/v1/sessions/\$id/complete"
completed=$(curl -sS -X POST "$api_url/api/v1/sessions/$sid/complete" \
	-H "Authorization: Bearer $token") || die "complete request failed"
echo "complete: state=$(printf '%s' "$completed" | json_field state)"

# --- 4. play the human: annotate a paragraph and submit ---------------------
step "simulating the human on $viewer_url"
page=$(curl -sS "$viewer_url") || die "could not fetch the viewer page"
session_token=$(printf '%s' "$page" | grep -o 'data-session-token="[^"]*"' | head -1 | sed 's/^data-session-token="//;s/"$//')
[ -n "$session_token" ] || die "could not find data-session-token in the viewer page"

page_flat=$(printf '%s' "$page" | tr '\n' ' ')
section=$(printf '%s' "$page_flat" | grep -o '<section class="md-block"[^>]*data-kind="paragraph"[^>]*>' | head -1 || true)
if [ -n "$section" ]; then
	block_id=$(printf '%s' "$section" | sed -n 's/.*id="\([^"]*\)".*/\1/p')
	start_line=$(printf '%s' "$section" | sed -n 's/.*data-start-line="\([^"]*\)".*/\1/p')
	end_line=$(printf '%s' "$section" | sed -n 's/.*data-end-line="\([^"]*\)".*/\1/p')
	quote=$(printf '%s' "$page_flat" | awk -v tag="$section" '
		{
			pos = index($0, tag)
			if (pos > 0) {
				rest = substr($0, pos + length(tag))
				if (match(rest, /<p>[^<]*<\/p>/)) {
					print substr(rest, RSTART + 3, RLENGTH - 7)
					exit
				}
			}
		}')
else
	block_id=block-unknown
	start_line=0
	end_line=0
	quote=
fi
[ -n "$quote" ] || quote="reviewed by the scripted round-trip"
echo "annotating block $block_id (lines ${start_line:-0}-${end_line:-0}): \"$quote\""

ann_body=$(printf '{"kind":"comment","block_id":%s,"start_line":%s,"end_line":%s,"quote":%s,"comment":%s}' \
	"$(printf '%s' "$block_id" | json_str)" \
	"${start_line:-0}" \
	"${end_line:-0}" \
	"$(printf '%s' "$quote" | json_str)" \
	"$(printf '%s' 'Looks good overall; one note from the scripted reviewer.' | json_str)")
annotation=$(curl -sS -X POST "${viewer_url}api/session/annotations" \
	-H "X-Yamdview-Token: $session_token" \
	-H 'Content-Type: application/json' \
	--data-binary "$ann_body") || die "annotation request failed"
ann_id=$(printf '%s' "$annotation" | json_field id)
[ -n "$ann_id" ] || die "no annotation id in response: $annotation"
echo "created annotation: $ann_id"

submit_body=$(printf '{"verdict":%s,"summary":%s}' \
	"$(printf '%s' 'Approve' | json_str)" \
	"$(printf '%s' 'Approved by the scripted reviewer; one comment attached.' | json_str)")
submitted=$(curl -sS -X POST "${viewer_url}api/session/submit" \
	-H "X-Yamdview-Token: $session_token" \
	-H 'Content-Type: application/json' \
	--data-binary "$submit_body") || die "submit request failed"
echo "submit:   $(printf '%s' "$submitted" | json_field state)"

# --- 5. long-poll feedback ---------------------------------------------------
step "GET /api/v1/sessions/\$id/feedback?wait=5s"
feedback=$(curl -sS "$api_url/api/v1/sessions/$sid/feedback?wait=5s" \
	-H "Authorization: Bearer $token") || die "feedback request failed"
printf '%s\n' "$feedback" | json_pretty

# --- 6. delete the session and shut down -------------------------------------
step "DELETE /api/v1/sessions/\$id"
status=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$api_url/api/v1/sessions/$sid" \
	-H "Authorization: Bearer $token") || die "delete request failed"
[ "$status" = "204" ] || die "delete returned HTTP $status, want 204"
echo "deleted ($status)"

printf '\n== round trip complete: presented, annotated, submitted, feedback collected\n'
exit 0
