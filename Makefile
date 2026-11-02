.PHONY: login jwt

# Use eval "$(make login)" to log in and export JWT in the current shell.
login:
	@docker compose exec vault vault login -no-print -method=oidc -path=oidc \
	  role=agentgateway skip_browser=true listenaddress=0.0.0.0 callbackhost=localhost >&2
	@$(MAKE) --no-print-directory jwt

# Reuse the stored Vault session: eval "$(make jwt)".
jwt:
	@token=$$(docker compose exec -T vault vault read -field=token identity/oidc/token/agentgateway) || exit $$?; \
	  test -n "$$token" || { echo 'Vault returned an empty JWT' >&2; exit 1; }; \
	  printf "export JWT='%s'\n" "$$token"
