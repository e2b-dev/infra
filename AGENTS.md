# AGENTS

## Integration tests
- Full suite: `make test-integration`
- Auto-resume proxy tests: `make -C tests/integration test/proxies/auto_resume_test.go`

## Environment
- Uses the last selected env from `.last_used_env` (e.g. `dev`).
- Switch env with `make set-env ENV=dev` (or `staging`, `prod`).
