# Prompt Graft
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/OneSol-Technologies/prompt-graft)

Prompt Graft is a Go reverse proxy that logs AI requests, injects prompt variants, and runs an asynchronous optimizer (GEPA-inspired) using user feedback. It is provider-agnostic and currently includes a Replicate adapter.

## Quick Start

1. Copy env template and set your keys:

```bash
cp .env.example .env
```

2. Start Redis + proxy + API + optimizer:

```bash
docker compose up --build
```

## How It Works (High Level)

- You send requests to the proxy instead of the provider.
- The proxy logs requests/responses and adds headers:
  - `X-PG-Session-Id` (session grouping)
  - `X-PG-Conversation-Id` (hash of first 5000 chars of prompt)
  - `X-PG-Variant-Id` (only when a variant is applied)
- Feedback is posted to the API and is later used by the optimizer to produce new variants.

## API Directory

Proxy (port 8080):
1. `POST /` (or any path) — forwards the request to the upstream provider and logs the interaction.

Feedback API (port 3001):
1. `POST /feedback/:sessionId` — submit feedback for a session (headers can target a specific conversation/variant).
2. `GET /sessions/:sessionId` — view session summary and latest prompt/feedback info.
3. `GET /variants/:sessionId` — view current variants, best prompt, and history.

Optimizer:
1. `docker compose --profile manual up --build optimizer` — runs a manual optimization cycle.

## Proxy Request Example (Replicate, wait for result)

This forwards directly to Replicate and waits for the final response.

```bash
curl -s -X POST \
  -H "Authorization: Bearer $REPLICATE_API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Prefer: wait" \
  -H "X-PG-Api-Style: replicate" \
  -H "X-PG-Session: demo-session-1" \
  -H "X-PG-Upstream-Url: https://api.replicate.com/v1/models/google/gemini-3.1-pro/predictions" \
  -d '{
    "input": {
      "prompt": "You are a helpful assistant, answer: why do stars twinkle?"
    }
  }' \
  http://localhost:8080
```

## Proxy Request Example (OpenAI-Style Chat via Bedrock)

```bash
curl -s --location 'http://localhost:8080' \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $BEDROCK_API_TOKEN" \
  -H "X-PG-Api-Style: openai-chat" \
  -H "X-PG-Session: bedrock-test-1" \
  -H "X-PG-Upstream-Url: https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1/chat/completions" \
  -d '{
    "model": "openai.gpt-oss-20b-1:0",
    "messages": [
      { "role": "user", "content": "Explain AI in simple terms" }
    ],
    "max_tokens": 200,
    "temperature": 0.7
  }'
```

## Second Request (Same Session)

```bash
curl -s -X POST \
  -H "Authorization: Bearer $REPLICATE_API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Prefer: wait" \
  -H "X-PG-Api-Style: replicate" \
  -H "X-PG-Session: demo-session-1" \
  -H "X-PG-Upstream-Url: https://api.replicate.com/v1/models/google/gemini-3.1-pro/predictions" \
  -d '{
    "input": {
      "prompt": "You are a helpful assistant, answer: how are cars made?"
    }
  }' \
  http://localhost:8080
```

## Send Feedback

Feedback is tied to the session. If you have the `X-PG-Conversation-Id` or `X-PG-Variant-Id` headers from the proxy response, pass them to target the exact run.

```bash
curl -s -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $YOUR_API_TOKEN" \
  -H "X-PG-Conversation-Id: <conversation_id_from_proxy_response>" \
  -H "X-PG-Variant-Id: <variant_id_from_proxy_response>" \
  -d '{"rating": 1}' \
  http://localhost:3001/feedback/demo-session-1
```

## API Styles (X-PG-Api-Style)

Available styles:
1. `replicate` — expects `input.prompt`
2. `openai-chat` — expects `messages[0].content` and concatenates `choices[].message.content`
3. `generic` — passthrough (no prompt extraction)

## Notes

- The proxy extracts system prompts from JSON payloads using provider adapters.
- Optimization runs asynchronously and never blocks the proxy request path.
- Response text is captured (when text/JSON) to improve prompt reflection.
