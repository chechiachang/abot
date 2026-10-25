#!/bin/sh
set -eu

: "${AZURE_OPENAI_BASE_URL:?set AZURE_OPENAI_BASE_URL}"
: "${AZURE_OPENAI_API_KEY:?set AZURE_OPENAI_API_KEY}"
: "${AZURE_OPENAI_MODEL:?set AZURE_OPENAI_MODEL}"

workdir=$(mktemp -d "${TMPDIR:-/tmp}/abot-foundry-smoke.XXXXXX")
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
git -C "$workdir" init --quiet

prompt_output=$(go run ./cmd/abot --config config.example.yaml --state-db "$workdir/state.sqlite" chat "Reply with exactly: foundry prompt smoke passed")
test "$prompt_output" = "foundry prompt smoke passed"
printf '%s\n' "$prompt_output"

go run ./cmd/abot --config config.example.yaml --state-db "$workdir/state.sqlite" --coding-workdir "$workdir" chat \
	"Delegate to the coding agent. In this repository, create smoke.txt containing exactly foundry-coding-smoke-ok, then use the bash tool to verify the content and run git diff --check. Report success only after both checks pass; do not change anything else."
test "$(cat "$workdir/smoke.txt")" = "foundry-coding-smoke-ok"
git -C "$workdir" diff --check
printf '%s\n' "coding smoke file and repository check passed"
