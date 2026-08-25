---
description: Present $ARGUMENTS for human review
---
Run `yamdview review --title "$ARGUMENTS" --prompt "Please review this document. Highlight anything wrong or unclear, add comments, then choose a verdict and submit." --choices "Approve,Request changes" --timeout 15m <file>` and parse the JSON feedback from stdout. Exit codes: 0 submitted, 2 timeout, 3 cancelled, 4 internal error. stdout carries only the JSON payload.
