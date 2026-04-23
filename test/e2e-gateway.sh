#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:10000}"
MCP_ENDPOINT="${GATEWAY_URL}/mcp"
PII_EMAIL="john@example.com"
PII_MESSAGE="My email is ${PII_EMAIL}"
EXPECTED_TOKEN="<EMAIL_ADDRESS>"

# 1. initialize — capture Mcp-Session-Id
INIT_RESP=$(curl -sS -i -X POST "$MCP_ENDPOINT" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
        "protocolVersion":"2024-11-05",
        "capabilities":{},
        "clientInfo":{"name":"e2e-gateway-test","version":"0.1"}}}')

SESSION_ID=$(echo "$INIT_RESP" | awk 'tolower($1)=="mcp-session-id:" {print $2}' | tr -d '\r\n')
[ -n "$SESSION_ID" ] || { echo "FAIL: no session id in initialize response"; echo "$INIT_RESP"; exit 1; }

# 2. initialized notification
curl -sS -X POST "$MCP_ENDPOINT" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' > /dev/null

# 3. tools/call echo with PII
RESP=$(curl -sS -X POST "$MCP_ENDPOINT" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d "$(jq -cn --arg m "$PII_MESSAGE" \
        '{jsonrpc:"2.0",id:2,method:"tools/call",
          params:{name:"echo",arguments:{message:$m}}}')")

# 4. assertions — handles both direct JSON and SSE (data: ...) framing
BODY=$(echo "$RESP" | sed -n 's/^data: //p' | tr -d '\r')
[ -n "$BODY" ] || BODY="$RESP"

if echo "$BODY" | grep -q "$PII_EMAIL"; then
  echo "FAIL: raw email leaked to client"
  echo "$BODY"
  exit 1
fi
if ! echo "$BODY" | grep -q "$EXPECTED_TOKEN"; then
  echo "FAIL: expected $EXPECTED_TOKEN in response"
  echo "$BODY"
  exit 1
fi
echo "PASS: echo returned masked message"
