#!/usr/bin/env bash
# examples/echo-agent/smoke.sh — end-to-end smoke test
#
# Boots ./bin/soyaos, hits the OpenAI-Compat gateway, the control RPC, and
# the Studio placeholder; verifies the echo agent comes back with the expected
# response on every entry-point.

set -euo pipefail

cd "$(dirname "$0")/../.."

bin="./bin/soyaos"
if [[ ! -x "$bin" ]]; then
  echo ">> building $bin"
  make build
fi

# Pick a pair of ports that won't collide with another local instance.
http_port=$((7474 + RANDOM % 1000))
rpc_port=$(( http_port + 1 ))
listen="127.0.0.1:${http_port}"
rpc="127.0.0.1:${rpc_port}"
base="http://${listen}"
ctl="http://${rpc}"
key="sk-soya-dev-local"
data_dir=$(mktemp -d)

echo ">> launching soyaos on ${listen} (rpc ${rpc}, data ${data_dir})"
"$bin" start --listen "$listen" --rpc "$rpc" --data-dir "$data_dir" >/tmp/soyaos-smoke.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; rm -rf "$data_dir"' EXIT

# Wait for both servers to come up.
for _ in $(seq 1 50); do
  if curl -fsS "${base}/healthz" >/dev/null 2>&1 && curl -fsS "${ctl}/control/v0/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
if ! curl -fsS "${base}/healthz" >/dev/null; then
  echo "FAIL: data-plane /healthz never responded"
  cat /tmp/soyaos-smoke.log
  exit 1
fi
if ! curl -fsS "${ctl}/control/v0/healthz" >/dev/null; then
  echo "FAIL: control-plane /control/v0/healthz never responded"
  cat /tmp/soyaos-smoke.log
  exit 1
fi

echo ">> GET /healthz (plain text)"
out=$(curl -fsS "${base}/healthz")
if [[ "$(echo "$out" | tr -d '[:space:]')" != "ok" ]]; then
  echo "FAIL: /healthz default did not return plain 'ok' — got: $out"
  exit 1
fi

echo ">> GET /healthz?format=json"
curl -fsS "${base}/healthz?format=json" | grep -q '"status":"ok"' \
  || { echo "FAIL: /healthz?format=json did not return JSON"; exit 1; }

echo ">> GET / (Studio placeholder)"
curl -fsS "${base}/" | grep -q "SoyaOS" \
  || { echo "FAIL: Studio placeholder missing 'SoyaOS'"; exit 1; }

echo ">> GET /v1/models"
curl -fsS -H "Authorization: Bearer ${key}" "${base}/v1/models" | grep -q "soya:echo" \
  || { echo "FAIL: /v1/models did not list soya:echo"; exit 1; }

echo ">> POST /v1/chat/completions (non-stream)"
out=$(curl -fsS -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \
       -d '{"model":"soya:echo","messages":[{"role":"user","content":"hi"}]}' \
       "${base}/v1/chat/completions")
grep -q "echo: hi" <<< "$out" \
  || { echo "FAIL: non-stream response did not contain 'echo: hi'"; echo "$out"; exit 1; }

echo ">> POST /v1/chat/completions (stream)"
out=$(curl -fsSN -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \
       -d '{"model":"soya:echo","stream":true,"messages":[{"role":"user","content":"ping"}]}' \
       "${base}/v1/chat/completions")
grep -q "echo: ping" <<< "$out" || { echo "FAIL: stream missing 'echo: ping'"; echo "$out"; exit 1; }
grep -q "\[DONE\]"      <<< "$out" || { echo "FAIL: stream missing [DONE]"; exit 1; }

echo ">> GET /control/v0/agents (control plane)"
curl -fsS "${ctl}/control/v0/agents" | grep -q "soya:echo" \
  || { echo "FAIL: control /agents did not list soya:echo"; exit 1; }

echo ">> POST /control/v0/agents/echo/invoke (control plane)"
out=$(curl -fsS -H "Content-Type: application/json" -d '{"prompt":"control"}' "${ctl}/control/v0/agents/echo/invoke")
grep -q "echo: control" <<< "$out" \
  || { echo "FAIL: control invoke did not echo"; echo "$out"; exit 1; }

echo ">> soyaos --spec-version"
spec_v=$("$bin" --spec-version)
[[ "$spec_v" == "cli.v0" ]] \
  || { echo "FAIL: --spec-version = '$spec_v', want 'cli.v0'"; exit 1; }

echo ">> data-dir persistence: bolt file exists + non-empty"
bolt_path="${data_dir}/soyaos.bolt"
[[ -f "$bolt_path" ]] || { echo "FAIL: $bolt_path missing"; exit 1; }
bolt_size=$(stat -f%z "$bolt_path" 2>/dev/null || stat -c%s "$bolt_path")
[[ "$bolt_size" -gt 0 ]] || { echo "FAIL: $bolt_path is empty"; exit 1; }

echo ">> persistence restart: kill soyaos, restart on same --data-dir, dev key still verifies"
kill "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true
"$bin" start --listen "$listen" --rpc "$rpc" --data-dir "$data_dir" >/tmp/soyaos-smoke-2.log 2>&1 &
pid=$!
for _ in $(seq 1 50); do
  if curl -fsS "${base}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
if ! curl -fsS "${base}/healthz" >/dev/null; then
  echo "FAIL: soyaos did not restart on same data-dir"
  cat /tmp/soyaos-smoke-2.log
  exit 1
fi
curl -fsS -H "Authorization: Bearer ${key}" "${base}/v1/models" | grep -q "soya:echo" \
  || { echo "FAIL: dev key did not verify after restart"; exit 1; }

echo "OK — echo-agent smoke test passed (data + control plane, plain healthz, Studio root, spec-version, bolt persistence + restart)"
