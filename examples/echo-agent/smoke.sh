#!/usr/bin/env bash
# examples/echo-agent/smoke.sh — end-to-end smoke test
#
# Boots ./bin/soyaos, hits /v1/models and /v1/chat/completions, verifies the
# echo agent comes back with the expected response, then shuts the server down.
# Exits non-zero if anything is off.

set -euo pipefail

cd "$(dirname "$0")/../.."

bin="./bin/soyaos"
if [[ ! -x "$bin" ]]; then
  echo ">> building $bin"
  make build
fi

# Pick a port that won't collide with another local instance.
port=$((6473 + RANDOM % 1000))
addr=":${port}"
base="http://localhost:${port}"
key="sk-soya-dev-local"

echo ">> launching soyaos on ${addr}"
"$bin" start --listen "$addr" >/tmp/soyaos-smoke.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT

# Wait for /healthz to come up.
for _ in $(seq 1 50); do
  if curl -fsS "${base}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
if ! curl -fsS "${base}/healthz" >/dev/null; then
  echo "FAIL: /healthz never responded"
  cat /tmp/soyaos-smoke.log
  exit 1
fi

echo ">> GET /v1/models"
curl -fsS -H "Authorization: Bearer ${key}" "${base}/v1/models" | grep -q "soya:echo" \
  || { echo "FAIL: /v1/models did not list soya:echo"; exit 1; }

echo ">> POST /v1/chat/completions (non-stream)"
out=$(curl -fsS -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \
       -d '{"model":"soya:echo","messages":[{"role":"user","content":"hi"}]}' \
       "${base}/v1/chat/completions")
if ! grep -q "echo: hi" <<< "$out"; then
  echo "FAIL: non-stream response did not contain 'echo: hi'"
  echo "$out"
  exit 1
fi

echo ">> POST /v1/chat/completions (stream)"
out=$(curl -fsSN -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \
       -d '{"model":"soya:echo","stream":true,"messages":[{"role":"user","content":"ping"}]}' \
       "${base}/v1/chat/completions")
if ! grep -q "echo: ping" <<< "$out"; then
  echo "FAIL: stream response missing 'echo: ping'"
  echo "$out"
  exit 1
fi
if ! grep -q "\[DONE\]" <<< "$out"; then
  echo "FAIL: stream missing [DONE] sentinel"
  echo "$out"
  exit 1
fi

echo "OK — echo-agent smoke test passed"
