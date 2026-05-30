# Agent instructions

- Never stage or commit `PLAN.md`. It is a local-only planning artifact and must stay out of git history.
- Use the project Makefile targets so Go module/build caches stay inside `.cache/`.
- Keep phase 0 intentionally minimal: bootstrap structure, path validation, and tests only.
