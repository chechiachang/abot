# Agentgateway demo

## Setup

```sh
make setup  # Python 3; generates secrets and copies missing configs
```

Set GitHub org/user, OAuth client credentials, and Azure API key in `.env`. [GitHub OAuth App](https://github.com/settings/developers) callback: `http://localhost:5556/dex/callback`; grant organization access.

```sh
docker compose up -d --force-recreate
```

## Login

```sh
eval "$(make login)"  # open the printed URL and sign in with GitHub
eval "$(make jwt)"    # refresh JWT using the stored Vault session
```

## Gateway UI

Open <http://localhost:4000/ui> → **Sign in with SSO** → GitHub. The same org/user restrictions apply; port 15000 is disabled.

[Models](http://localhost:4000/ui/llm/models): `gpt-6-luna`, `gpt-6.1-sol`, `text-embedding-3-large`, `decision-1` (SystemOne: native `state`/`questions`, not chat messages).

## Local model tests

After login (requires `curl` and Python 3):

```sh
sh scripts/check-auth.sh
sh scripts/check-models.sh                         # chat, embeddings, and SystemOne
sh scripts/check-models.sh systemone-request.json  # optional custom request
```

## Tools, MCP, and agents

MCP at `http://localhost:4000/mcp` exposes `playground_calculate` (arithmetic), `playground_text_stats` (counts), and `playground_reverse_text` (Unicode text reversal). A2A agents at `/agents/calculator`, `/agents/text-analyst`, and `/agents/text-transformer` accept text via `message/send`; cards are at each agent's `/.well-known/agent-card.json`. All require `Authorization: Bearer $JWT`. These deterministic examples use no model quota.

Existing setups: merge the `mcp` and `routes` sections from [the gateway template](config/agentgateway.yaml.example) into `config/agentgateway.yaml`. `make setup` preserves existing configs.

```sh
docker compose up -d --build
eval "$(make login)"
python3 scripts/check-examples.py  # auth, MCP discovery/calls, all agents
```

## Codex

Launch without changing global config:

```sh
eval "$(make login)"
codex -m gpt-6-luna \
  -c 'model_provider="agw"' \
  -c 'model_providers.agw={name="Agentgateway",base_url="http://localhost:4000/v1",env_key="JWT",wire_api="responses",requires_openai_auth=false}'
```

Alternatively, merge [the provider example](examples/codex-config.toml) into `~/.codex/config.toml`, then run `codex` after login. Provider/auth settings are not loaded from project config; `--config` accepts key/value overrides, not a file path.

Dev-only. Restarting Vault resets sessions; inference uses Azure quota. [Security](SECURITY.md).

### Specialist agents

The same Python stdlib service provides three deterministic, no-credential A2A agents at `/agents/research`, `/agents/data-analyst`, and `/agents/issue-triage`; each card is published at `/agents/<name>/.well-known/agent-card.json`. The routes inherit the gateway's strict Vault JWT requirement. Merge the updated routes from `config/agentgateway.yaml.example` into an existing local gateway config; `make setup` will not overwrite it. Build/restart with `docker compose up -d --build`, then use the A2A `message/send` JSON-RPC interface or existing MCP tools.

- **Research** accepts one HTTPS URL. It only permits `www.iana.org`, `www.rfc-editor.org`, and `www.python.org`, uses port 443, checks every resolved address is globally routable, times out after 3 seconds, accepts only text/plain or text/html, caps responses at 64 KiB, and does not follow redirects. Output text is capped at 16 KiB. This is a small public-source fetcher, not a crawler.
- **Data analyst** accepts CSV or JSON (JSON array of objects) through `data_summary`; max 64 KiB input, 1,000 rows, 50 columns, and 4 KiB per field. Reports missing/distinct counts, numeric min/max/mean/median, or top categorical values. A2A message text is JSON: `{"format":"csv","data":"value\\n1\\n2\\n"}` (JSON format accepts `data` as an array or encoded JSON string).
- **Issue triage** picks only `platform`, `security`, `documentation`, or `product`, and returns a keyword-based rationale and heuristic confidence. It is a suggestion only: it cannot authorize, assign, or perform actions.

All three are covered by offline unit tests and need no model credentials. Run `python3 -B -m unittest discover -s tests -v`; after login, `python3 scripts/check-examples.py` also exercises their gateway cards/routes (the research request is intentionally not made against live internet).
