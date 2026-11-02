# Agentgateway demo

## Setup

```sh
cp .env.example .env
for name in agentgateway.yaml dex.yaml vault.hcl; do
  cp "config/$name.example" "config/$name"
done
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

## Codex

Launch without changing global config:

```sh
eval "$(make login)"
codex -m gpt-6-luna \
  -c 'model_provider="agw"' \
  -c 'model_providers.agw={name="Agentgateway",base_url="http://localhost:4000/v1",env_key="JWT",wire_api="responses",requires_openai_auth=false}'
```

Alternatively, merge [the provider example](examples/codex-config.toml) into `~/.codex/config.toml`, then run `codex` after login. Provider/auth settings are not loaded from project config; `--config` accepts key/value overrides, not a file path.

Dev-only. Restarting Vault resets sessions; inference uses Azure quota.
