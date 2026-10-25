# abot

abot is a Go-based AI assistant for Telegram and the command line. It can answer questions, work with files in a configured workspace, and use optional integrations.

## Features

- Persistent conversations with per-chat access and tool policies.
- Coding tools for reading, editing, writing, and running commands in a workspace.
- URL, image, document, and audio input.
- Optional MCP tools, market quotes, and public Polymarket data.

## Quick start

Requires Go 1.26.5 and an Azure OpenAI deployment.

```sh
cp .env.example .env
cp config.example.yaml config.yaml
# Set Azure OpenAI values in .env
go mod download
go run ./cmd/abot --config config.yaml config validate
go run ./cmd/abot --config config.yaml chat "Summarize this repository."
```

To run the Telegram bot, set `TELEGRAM_BOT_TOKEN` and configure allowed users in `config.yaml`, then run:

```sh
go run ./cmd/abot --config config.yaml telegram serve
```

Telegram access is deny-by-default. See `go run ./cmd/abot --help` for other commands. For Docker Compose, run `docker compose up --build`.

## GitHub writes

The coding shell can use `gh` and Git to push changes and open PRs. For local runs, install `gh` and authenticate as the OS user running abot:

```sh
gh auth login --hostname github.com --git-protocol https --with-token < /secure/path/to/pat-file
gh auth setup-git
```

Use a fine-grained PAT limited to the target repository with **Contents: Read and write** and **Pull requests: Read and write**. Keep the temporary PAT file outside the workspace and remove it after login; `gh` stores credentials for that OS user. The coding shell can access those credentials. The Docker image does not include `gh`.
