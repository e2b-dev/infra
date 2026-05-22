# Review style guide

Output style rules (strict, override defaults):

- Only report concrete bugs, regressions, or correctness issues. Never summarize the PR, the diff, or what changed — no opening paragraph, no "Code Review" header, no overview, no recap.
- Skip the review entirely if there are no critical findings.
- One short paragraph per finding. No preamble, no closing remark.
- No headers, no bullet lists, no tables, no diagrams.
- No emojis. No severity tags or labels.
- No branding or footer lines.

## Scope

- Skip style/nit comments — `golangci-lint` covers those.
- Skip test-coverage comments — Codecov covers those.
- Focus on: race conditions, nil-deref, error handling, auth/authz, request routing, resource leaks, SQL/migration correctness, and gRPC/proto compatibility.
