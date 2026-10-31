# abot

**A persistent AI assistant for Telegram and the command line, built in Go.**

abot brings a practical agent workflow to the places you work: chat with it, give it a workspace, and let it use carefully configured tools to help with code, files, links, and media.

[![Go checks](https://github.com/chechiachang/abot/actions/workflows/ci.yml/badge.svg)](https://github.com/chechiachang/abot/actions/workflows/ci.yml)
[![Security scan](https://github.com/chechiachang/abot/actions/workflows/security.yml/badge.svg)](https://github.com/chechiachang/abot/actions/workflows/security.yml)

## What it can do

- **Chat across CLI and Telegram** with persistent conversations and configurable per-chat policies.
- **Work in a coding workspace** using file read, edit, write, and command tools.
- **Understand shared content** including URLs, images, documents, and audio.
- **Extend with integrations** for MCP tools, market quotes, and public Polymarket data.
- **Operate with access controls**; Telegram access is deny-by-default, and optional integrations can stay disabled.

## Get started

Requires **Go 1.26.10** and an Azure OpenAI deployment.

```sh
git clone https://github.com/chechiachang/abot.git
cd abot
cp .env.example .env
cp config.example.yaml config.yaml
```

Set the Azure OpenAI values in `.env`, then validate configuration and try a CLI prompt:

```sh
go mod download
go run ./cmd/abot --config config.yaml config validate
go run ./cmd/abot --config config.yaml chat "Summarize this repository."
```

To run Telegram, set `TELEGRAM_BOT_TOKEN` in `.env` and add your numeric Telegram user ID to `telegram.allowed_user_ids` in `config.yaml` (the default is empty):

```sh
go run ./cmd/abot --config config.yaml telegram serve
```

For a container deployment, configure `.env` and `config.yaml`, then start with `docker compose up --build`. See [deployment instructions](deploy/README.md) for a systemd installation.

## Configuration and commands

Use `go run ./cmd/abot --help` to explore commands. The checked-in [`config.example.yaml`](config.example.yaml), [`.env.example`](.env.example), and [`SYSTEM.md`](SYSTEM.md) document the available configuration and assistant instructions. Keep credentials in environment variables or a protected secrets store—never commit them.

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/abot
```

Pull requests run these checks and a free dependency/code security scan. See [CI workflows](.github/workflows/) for details.

## Project guides

- [Deploy with systemd](deploy/README.md)
- [Implementation task index](docs/tasks/README.md)
- [Project plan](docs/plan.md)
