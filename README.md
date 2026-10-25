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
