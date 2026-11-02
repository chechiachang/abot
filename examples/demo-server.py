#!/usr/bin/env python3
"""Dependency-free MCP tools and deterministic A2A demo agents (no model quota)."""
import ast
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import math
import operator
import os
from uuid import uuid4

BASE_URL = os.environ.get("DEMO_PUBLIC_URL", "http://localhost:4000").rstrip("/")
OPERATORS = {ast.Add: operator.add, ast.Sub: operator.sub, ast.Mult: operator.mul,
             ast.Div: operator.truediv, ast.Mod: operator.mod}
TOOLS = [
    {"name": "calculate", "description": "Evaluate arithmetic (+, -, *, /, %, parentheses).",
     "inputSchema": {"type": "object", "properties": {"expression": {"type": "string", "maxLength": 200}},
                     "required": ["expression"], "additionalProperties": False}},
    {"name": "text_stats", "description": "Count characters, words, and lines in text.",
     "inputSchema": {"type": "object", "properties": {"text": {"type": "string", "maxLength": 10000}},
                     "required": ["text"], "additionalProperties": False}},
    {"name": "reverse_text", "description": "Reverse text by Unicode code point.",
     "inputSchema": {"type": "object", "properties": {"text": {"type": "string", "maxLength": 10000}},
                     "required": ["text"], "additionalProperties": False}},
]
AGENTS = {
    "calculator": {"name": "Calculator", "description": "Evaluates arithmetic without an LLM.",
                   "examples": ["(12 + 8) * 3"], "tags": ["math", "tools"]},
    "text-analyst": {"name": "Text analyst", "description": "Counts text characters, words, and lines without an LLM.",
                     "examples": ["Hello from agentgateway"], "tags": ["text", "tools"]},
    "text-transformer": {"name": "Text transformer", "description": "Reverses text by Unicode code point without an LLM.",
                          "examples": ["agentgateway"], "tags": ["text", "transform"]},
}


def calculate(expression):
    if not isinstance(expression, str) or not expression.strip() or len(expression) > 200:
        raise ValueError("expression must be a nonempty string of at most 200 characters")

    def evaluate(node):
        if isinstance(node, ast.Constant) and type(node.value) in (int, float):
            value = node.value
        elif isinstance(node, ast.BinOp) and type(node.op) in OPERATORS:
            value = OPERATORS[type(node.op)](evaluate(node.left), evaluate(node.right))
        elif isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub)):
            value = evaluate(node.operand) * (-1 if isinstance(node.op, ast.USub) else 1)
        else:
            raise ValueError("Only numbers and +, -, *, /, %, parentheses are supported")
        if not math.isfinite(value) or abs(value) > 1e100:
            raise ValueError("Result is outside the supported numeric range")
        return value

    return {"value": evaluate(ast.parse(expression.strip(), mode="eval").body)}


def text_stats(text):
    if not isinstance(text, str) or len(text) > 10000:
        raise ValueError("text must be a string of at most 10000 characters")
    return {"characters": len(text), "words": len(text.split()), "lines": len(text.splitlines())}


def reverse_text(text):
    if not isinstance(text, str) or len(text) > 10000:
        raise ValueError("text must be a string of at most 10000 characters")
    return {"text": text[::-1]}


def call_tool(name, arguments):
    if not isinstance(arguments, dict):
        raise ValueError("arguments must be an object")
    if name == "calculate" and set(arguments) == {"expression"}:
        return calculate(arguments["expression"])
    if name == "text_stats" and set(arguments) == {"text"}:
        return text_stats(arguments["text"])
    if name == "reverse_text" and set(arguments) == {"text"}:
        return reverse_text(arguments["text"])
    raise ValueError("Unknown tool or invalid arguments")


def agent_card(name):
    agent = AGENTS[name]
    return {"protocolVersion": "0.3.0", "name": agent["name"], "description": agent["description"],
            "url": f"{BASE_URL}/agents/{name}", "version": "1.0.0", "preferredTransport": "JSONRPC",
            "capabilities": {"streaming": False, "pushNotifications": False},
            "defaultInputModes": ["text/plain"], "defaultOutputModes": ["text/plain"],
            "securitySchemes": {"vaultJwt": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}},
            "security": [{"vaultJwt": []}],
            "skills": [{"id": name, **agent, "inputModes": ["text/plain"], "outputModes": ["text/plain"]}]}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        pass  # Never log request bodies or bearer tokens.

    def reply(self, status, body=None):
        data = b"" if body is None else json.dumps(body, allow_nan=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/health":
            self.reply(200, {"status": "ok"})
            return
        for name in AGENTS:
            if self.path in (f"/agents/{name}/.well-known/agent-card.json",
                             f"/agents/{name}/.well-known/agent.json"):
                self.reply(200, agent_card(name))
                return
        self.reply(405 if self.path == "/mcp" else 404)

    def do_DELETE(self):
        self.reply(405)  # Stateless MCP: no sessions to terminate.

    def do_POST(self):
        agent = self.path.removeprefix("/agents/")
        if self.path != "/mcp" and agent not in AGENTS:
            self.reply(404)
            return
        request_id = None
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 65536:
                self.close_connection = True
                self.reply(413)
                return
            try:
                request = json.loads(self.rfile.read(length))
            except (ValueError, UnicodeDecodeError):
                self.rpc_error(None, -32700, "Parse error")
                return
            if not isinstance(request, dict) or request.get("jsonrpc") != "2.0" or not isinstance(request.get("method"), str):
                self.rpc_error(None, -32600, "Invalid Request")
                return
            request_id = request.get("id")
            method = request["method"]
            if "id" not in request:
                self.reply(202)
                return
            params = request.get("params", {})
            if not isinstance(params, dict):
                raise ValueError("params must be an object")
            if self.path == "/mcp":
                result = self.mcp(method, params)
            else:
                result = self.a2a(agent, method, params)
            if result is None:
                self.rpc_error(request_id, -32601, "Method not found")
            else:
                self.reply(200, {"jsonrpc": "2.0", "id": request_id, "result": result})
        except (ValueError, SyntaxError, ArithmeticError, RecursionError) as error:
            self.rpc_error(request_id, -32602, str(error))

    def rpc_error(self, request_id, code, message):
        self.reply(200, {"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}})

    def mcp(self, method, params):
        if method == "initialize":
            version = params.get("protocolVersion")
            return {"protocolVersion": version if version in ("2024-11-05", "2025-03-26", "2025-06-18") else "2025-06-18",
                    "capabilities": {"tools": {"listChanged": False}},
                    "serverInfo": {"name": "playground-tools", "version": "1.0.0"}}
        if method == "ping":
            return {}
        if method == "tools/list":
            return {"tools": TOOLS}
        if method == "tools/call":
            try:
                value = call_tool(params.get("name"), params.get("arguments", {}))
                return {"content": [{"type": "text", "text": json.dumps(value)}], "isError": False}
            except (ValueError, SyntaxError, ArithmeticError, RecursionError) as error:
                return {"content": [{"type": "text", "text": str(error)}], "isError": True}
        return None

    def a2a(self, agent, method, params):
        if method != "message/send":
            return None
        message = params.get("message")
        if not isinstance(message, dict) or message.get("role") != "user" or not isinstance(message.get("messageId"), str):
            raise ValueError("message requires role=user and a messageId")
        parts = message.get("parts")
        if not isinstance(parts, list) or not parts or any(
            not isinstance(part, dict) or part.get("kind") != "text" or not isinstance(part.get("text"), str)
            for part in parts
        ):
            raise ValueError("message.parts must contain text parts")
        text = "\n".join(part["text"] for part in parts)
        value = {"calculator": calculate, "text-analyst": text_stats, "text-transformer": reverse_text}[agent](text)
        return {"kind": "message", "role": "agent", "messageId": str(uuid4()),
                "parts": [{"kind": "text", "text": json.dumps(value)}]}


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
