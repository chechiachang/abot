# Guidelines

- Less is good: keep code simple and compact.
- Keep README content minimal: only what is needed to do the work.
- Use brief comments only when the intent is not obvious.

## Development workflow

1. Read `docs/plan.md` and relevant code; run `git status` before editing. Preserve unrelated changes.
2. Define the task scope and acceptance criteria. Keep changes small and focused.
3. Set up dependencies and local configuration. Never commit secrets.
4. Implement the change; update necessary docs and meaningful tests.
5. Format, lint, test, and build. Fix failures before proceeding; report unavailable checks.
6. Review the diff and run `git diff --check`.
7. When asked to prepare a commit, fetch the issue with `gh issue view <number> --json title,body`; use `feat/<number>-<description>`, `fix/<number>-<description>`, or `chore/<number>-<description>`.
8. Stage relevant files, commit as `feat <number>: <description>`, push the branch, and create a PR against `master` with the same title and reviewer `copilot`. If a command fails, stop and provide exact manual completion commands.
9. Summarize changes, checks, and remaining work. Release/deploy only when requested.

## Commands

- Setup: `go mod download`
- Run locally: `go run ./cmd/abot chat "<prompt>"` (loads `.env` automatically; requires Azure OpenAI configuration)
- Format: `gofmt -w cmd internal`
- Lint: `go vet ./...`
- Test: `go test ./...`
- Build: `go build -o /tmp/abot ./cmd/abot`
- Container: `docker compose up --build`
- Release: push a `v*` tag to publish Go archives and the GHCR image; deployment is not configured.
