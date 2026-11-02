#!/usr/bin/env python3
"""Exercise MCP tools and A2A agents through AGW after make login (stdlib only)."""
import json
import os
import sys
from urllib.error import HTTPError
from urllib.request import Request, urlopen
from uuid import uuid4

BASE_URL = os.environ.get("AGW_URL", "http://localhost:4000").rstrip("/")
JWT = os.environ.get("JWT")
if not JWT:
    sys.exit('JWT is required; run eval "$(make login)" first')
SESSION = None


def request(path, body=None, authenticated=True):
    global SESSION
    headers = {"Accept": "application/json, text/event-stream"}
    if authenticated:
        headers["Authorization"] = "Bearer " + JWT
    if path == "/mcp" and SESSION:
        headers["Mcp-Session-Id"] = SESSION
        headers["MCP-Protocol-Version"] = "2025-06-18"
    if body is not None:
        headers["Content-Type"] = "application/json"
    data = None if body is None else json.dumps(body).encode()
    with urlopen(Request(BASE_URL + path, data=data, headers=headers), timeout=20) as response:
        if path == "/mcp" and authenticated:
            SESSION = response.headers.get("Mcp-Session-Id", SESSION)
        text = response.read().decode()
        if not text:
            return None
        if "text/event-stream" in response.headers.get("Content-Type", ""):
            for event in text.replace("\r\n", "\n").split("\n\n"):
                payload = "\n".join(line[5:].lstrip() for line in event.splitlines() if line.startswith("data:"))
                if payload:
                    result = json.loads(payload)
                    if result.get("id") == body.get("id"):
                        return result
            raise AssertionError("Missing MCP response in event stream")
        return json.loads(text)


def rpc(path, method, params, request_id=1):
    result = request(path, {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
    assert result is not None and "error" not in result, result
    return result["result"]


for path, body in [
    ("/mcp", {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}),
    ("/agents/calculator/.well-known/agent-card.json", None),
    ("/agents/text-analyst/.well-known/agent-card.json", None),
    ("/agents/text-transformer/.well-known/agent-card.json", None),
    ("/agents/research/.well-known/agent-card.json", None),
    ("/agents/data-analyst/.well-known/agent-card.json", None),
    ("/agents/issue-triage/.well-known/agent-card.json", None),
    ("/agents/calculator", {"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": {}}),
    ("/agents/text-analyst", {"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": {}}),
    ("/agents/text-transformer", {"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": {}}),
    ("/agents/research", {"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": {}}),
    ("/agents/data-analyst", {"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": {}}),
    ("/agents/issue-triage", {"jsonrpc": "2.0", "id": 1, "method": "message/send", "params": {}}),
]:
    try:
        request(path, body, authenticated=False)
    except HTTPError as error:
        assert error.code in (401, 403), (path, error.code)
    else:
        raise AssertionError(f"Unauthenticated request accepted: {path}")
print("PASS JWT required for MCP, agent cards, and agent messages")

rpc("/mcp", "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                          "clientInfo": {"name": "check-examples", "version": "1.0.0"}})
request("/mcp", {"jsonrpc": "2.0", "method": "notifications/initialized"})
tools = rpc("/mcp", "tools/list", {})["tools"]
for suffix, arguments, expected in [
    ("calculate", {"expression": "(12 + 8) * 3"}, {"value": 60}),
    ("text_stats", {"text": "Hello from agentgateway"}, {"characters": 23, "words": 3, "lines": 1}),
    ("reverse_text", {"text": "agentgateway"}, {"text": "yawetagtnega"}),
]:
    name = next(tool["name"] for tool in tools if tool["name"].endswith(suffix))
    result = rpc("/mcp", "tools/call", {"name": name, "arguments": arguments})
    assert not result.get("isError"), result
    assert json.loads(result["content"][0]["text"]) == expected, result
    print(f"PASS MCP {name}")

for name, text, expected in [
    ("calculator", "(12 + 8) * 3", {"value": 60}),
    ("text-analyst", "Hello from agentgateway", {"characters": 23, "words": 3, "lines": 1}),
    ("text-transformer", "agentgateway", {"text": "yawetagtnega"}),
    ("data-analyst", json.dumps({"format": "csv", "data": "n\n1\n3\n"}), {"rows": 2}),
    ("issue-triage", "Deployment gateway crash", {"team": "platform"}),
]:
    path = "/agents/" + name
    card = request(path + "/.well-known/agent-card.json")
    assert card["url"].endswith(path) and card["security"], card
    result = rpc(path, "message/send", {"message": {
        "kind": "message", "role": "user", "messageId": str(uuid4()),
        "parts": [{"kind": "text", "text": text}],
    }})
    parsed_result = json.loads(result["parts"][0]["text"])
    if name == "data-analyst":
        assert parsed_result["rows"] == expected["rows"], parsed_result
    elif name == "issue-triage":
        assert parsed_result["team"] == expected["team"] and parsed_result["heuristic"], parsed_result
    else:
        assert parsed_result == expected, result
    assert result["role"] == "agent", result
    print(f"PASS A2A {name}")
