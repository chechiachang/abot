#!/bin/sh
set -eu

: "${JWT:?Authenticate with Vault and export JWT first; see README.md}"
if [ "$#" -gt 1 ]; then
  echo "Usage: sh $0 [systemone-request.json]" >&2
  exit 1
fi
command -v curl >/dev/null
command -v python3 >/dev/null

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
systemone_request=${1:-"$script_dir/../examples/systemone-request.json"}
[ -r "$systemone_request" ] || { echo "Cannot read SystemOne request: $systemone_request" >&2; exit 1; }
python3 - "$systemone_request" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    request = json.load(f)
if not isinstance(request, dict) or request.get('model') != 'decision-1':
    raise SystemExit('SystemOne request must be a JSON object with model decision-1')
PY

base_url=http://localhost:4000
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

request() {
  label=$1
  path=$2
  shift 2
  status=$(curl --silent --show-error --connect-timeout 5 --max-time 180 \
    --output "$work_dir/response.json" --write-out '%{http_code}' \
    -H "Authorization: Bearer $JWT" "$base_url$path" "$@")
  if [ "$status" != 200 ]; then
    echo "FAIL $label: HTTP $status" >&2
    cat "$work_dir/response.json" >&2
    echo >&2
    exit 1
  fi
}

request 'Model catalog' /v1/models
python3 - "$work_dir/response.json" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    response = json.load(f)
required = {'gpt-6-luna', 'gpt-6.1-sol', 'text-embedding-3-large', 'decision-1'}
listed = {model.get('id') for model in response.get('data', [])}
missing = required - listed
if missing:
    raise SystemExit('FAIL model catalog: missing ' + ', '.join(sorted(missing)))
print('PASS model catalog: all four models are listed')
PY

for model in gpt-6-luna gpt-6.1-sol; do
  payload=$(printf '{"model":"%s","messages":[{"role":"user","content":"Reply with exactly OK."}],"stream":false}' "$model")
  request "$model inference" /v1/chat/completions \
    -H 'Content-Type: application/json' --data "$payload"
  python3 - "$work_dir/response.json" "$model" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    response = json.load(f)
model = sys.argv[2]
choices = response.get('choices', [])
if response.get('error') or not choices or not isinstance(choices[0].get('message'), dict):
    raise SystemExit(f'FAIL {model}: no successful chat completion in response')
print(f'PASS {model}: chat completion returned')
print(choices[0]['message'].get('content') or choices[0]['message'].get('refusal') or '')
PY
done

request 'decision-1 inference' /providers/microsoft/v1/systemone \
  -H 'Content-Type: application/json' --data-binary "@$systemone_request"
python3 - "$work_dir/response.json" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    response = json.load(f)
if not response or (isinstance(response, dict) and response.get('error')):
    raise SystemExit('FAIL decision-1: empty or error response')
print('PASS decision-1: SystemOne returned HTTP 200 and JSON')
print(json.dumps(response, indent=2))
PY

request 'text-embedding-3-large inference' /v1/embeddings \
  -H 'Content-Type: application/json' \
  --data '{"model":"text-embedding-3-large","input":"The API returns 500 on every call.","encoding_format":"float"}'
python3 - "$work_dir/response.json" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    response = json.load(f)
data = response.get('data', [])
embedding = data[0].get('embedding') if data else None
if response.get('error') or not isinstance(embedding, list) or not embedding or not all(isinstance(x, (int, float)) for x in embedding):
    raise SystemExit('FAIL text-embedding-3-large: no embedding vector in response')
print(f'PASS text-embedding-3-large: {len(embedding)}-dimension embedding returned')
PY
