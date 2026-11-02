# Authorized GitHub users may mint only the gateway audience's JWT.
path "identity/oidc/token/agentgateway" {
  capabilities = ["read"]
}
