#!/bin/sh
set -eu

: "${GITHUB_ORG:?Set GITHUB_ORG in .env}"
: "${GITHUB_USER:?Set GITHUB_USER in .env}"
: "${DEX_VAULT_CLIENT_SECRET:?Set DEX_VAULT_CLIENT_SECRET in .env}"
if [ "$GITHUB_ORG" = your-github-org ] || [ "$GITHUB_USER" = your-github-username ]; then
  echo 'Edit GITHUB_ORG and GITHUB_USER in .env before starting the demo.' >&2
  exit 1
fi

for name in "$GITHUB_ORG" "$GITHUB_USER"; do
  case "$name" in
    *[!A-Za-z0-9-]*) echo 'GitHub org/user must contain only letters, digits and hyphens.' >&2; exit 1 ;;
  esac
done

vault policy write agentgateway /demo/vault-agentgateway-policy.hcl

# Safe to rerun while the dev Vault is still running.
if ! vault auth list -format=json | grep -q '"oidc/"'; then
  vault auth enable -path=oidc oidc
fi
# Dex starts alongside this setup job. Discovery must work before configuring OIDC.
attempt=0
until vault write auth/oidc/config \
  oidc_discovery_url=http://localhost:5556/dex \
  oidc_client_id=vault \
  oidc_client_secret="$DEX_VAULT_CLIENT_SECRET" \
  default_role=agentgateway; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo 'Could not configure Vault OIDC; check docker compose logs dex.' >&2
    exit 1
  fi
  sleep 1
done

# Dex verifies organization membership; Vault independently restricts the username.
vault write auth/oidc/role/agentgateway - <<EOF
{
  "role_type": "oidc",
  "user_claim": "sub",
  "bound_audiences": ["vault"],
  "bound_claims": {"preferred_username": "$GITHUB_USER"},
  "oidc_scopes": ["profile", "email"],
  "allowed_redirect_uris": [
    "http://localhost:8250/oidc/callback",
    "http://localhost:8200/ui/vault/auth/oidc/oidc/callback"
  ],
  "token_policies": ["agentgateway"],
  "token_ttl": "1h",
  "token_max_ttl": "1h",
  "token_no_default_policy": true
}
EOF

vault write identity/oidc/key/agentgateway \
  algorithm=RS256 allowed_client_ids=agentgateway
vault write identity/oidc/role/agentgateway \
  key=agentgateway client_id=agentgateway ttl=1h

echo 'Vault browser OIDC auth and agentgateway JWT issuance configured.'
