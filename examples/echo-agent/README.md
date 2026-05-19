# echo-agent — SoyaOS OpenAI-Compat smoke test

The first runnable SoyaOS Agent. It does nothing intelligent — it replies to
whatever the user said with `echo: <message>` — and exists for a single
reason: to prove that the OpenAI-Compat gateway, the kernel routing, and the
auth path are all wired correctly.

If this Agent works end-to-end through `curl` and through an off-the-shelf
OpenAI client, then **every** future Agent (Compo, NewsBeam, EstateMuse,
SilentCut) has somewhere stable to plug in.

## 30-second quickstart

```bash
# 1. Build the soyaos binary
make build

# 2. Boot Solo all-in-one (it prints the dev API key on startup)
./bin/soyaos start
```

In another terminal:

```bash
# 3. List available "models" (== registered Agents)
curl http://localhost:6473/v1/models \
  -H "Authorization: Bearer sk-soya-dev-local"

# 4. Talk to the echo agent
curl http://localhost:6473/v1/chat/completions \
  -H "Authorization: Bearer sk-soya-dev-local" \
  -H "Content-Type: application/json" \
  -d '{
        "model": "soya:echo",
        "messages": [{"role":"user","content":"hello"}]
      }'

# 5. Streaming (SSE) form
curl -N http://localhost:6473/v1/chat/completions \
  -H "Authorization: Bearer sk-soya-dev-local" \
  -H "Content-Type: application/json" \
  -d '{
        "model": "soya:echo",
        "stream": true,
        "messages": [{"role":"user","content":"hello"}]
      }'
```

You should see `echo: hello` come back.

## With an OpenAI SDK / Cherry Studio / Cursor

Any tool that takes a base URL + an API key works:

- Base URL: `http://localhost:6473/v1`
- API key:  `sk-soya-dev-local`
- Model:    `soya:echo`

## Source

The echo Agent itself is built into the binary at
[`pkg/kernel/echo_agent.go`](../../pkg/kernel/echo_agent.go). When external
Agents land, they will live under `examples/` as separate Go programs that
embed the SDK.
