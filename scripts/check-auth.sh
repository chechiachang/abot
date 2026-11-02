#!/bin/sh
set -eu

: "${JWT:?Set JWT to a Vault-issued agentgateway identity token}"
url=http://localhost:4000/v1/models

check() {
  expected=$1
  label=$2
  shift 2
  actual=$(curl --silent --show-error --max-time 10 --output /dev/null \
    --write-out '%{http_code}' "$url" "$@")
  if [ "$actual" != "$expected" ]; then
    echo "$label: expected $expected, got $actual" >&2
    exit 1
  fi
  echo "$label: $actual"
}

check 401 'No token'
check 401 'Invalid token' -H 'Authorization: Bearer invalid'
check 200 'Vault identity JWT' -H "Authorization: Bearer $JWT"

url=http://localhost:4000/providers/microsoft/v1/systemone
check 401 'SystemOne: no token' --request POST
check 401 'SystemOne: invalid token' --request POST -H 'Authorization: Bearer invalid'

url=http://localhost:4000/ui
check 302 'UI: browser sign-in required' \
  -H 'Accept: text/html' -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document'
url=http://localhost:4000/api/config
check 401 'UI API: session required' \
  -H 'Accept: application/json' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Dest: empty'
