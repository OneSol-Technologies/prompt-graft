# Prompt Graft

Prompt Graft is a Go reverse proxy that logs AI requests, injects prompt variants, and runs an asynchronous optimizer (GEPA-inspired) using user feedback. It is provider-agnostic and currently includes a Replicate adapter.

## Quick Start

1. Copy env template and set your keys:

```bash
cp .env.example .env
```

2. Start Redis + proxy + API (optimizer optional):

```bash
docker compose up --build
```

3. (Optional) Run optimizer manually when you want:

```bash
docker compose --profile manual up --build optimizer
```

## How It Works (High Level)

- You send requests to the proxy instead of the provider.
- The proxy logs requests/responses and adds headers:
  - `X-PG-Session-Id` (session grouping)
  - `X-PG-Conversation-Id` (hash of first 5000 chars of prompt)
  - `X-PG-Variant-Id` (only when a variant is applied)
- Feedback is posted to the API and is later used by the optimizer to produce new variants.

## Proxy Request Example (Replicate, wait for result)

This forwards directly to Replicate and waits for the final response.

```bash
curl -s -X POST \
  -H "Authorization: Bearer $REPLICATE_API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Prefer: wait" \
  -H "X-PG-Session: demo-session-1" \
  -H "X-PG-Upstream-Url: https://api.replicate.com/v1/models/google/gemini-3.1-pro/predictions" \
  -d '{
    "input": {
      "prompt": "You are a helpful assistant, answer: why do stars twinkle?"
    }
  }' \
  http://localhost:8080
```

## Second Request (Same Session)

```bash
curl -s -X POST \
  -H "Authorization: Bearer $REPLICATE_API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Prefer: wait" \
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
  -H "X-PG-Conversation-Id: <conversation_id_from_proxy_response>" \
  -H "X-PG-Variant-Id: <variant_id_from_proxy_response>" \
  -d '{"rating": 1}' \
  http://localhost:3001/feedback/demo-session-1
```

## Notes

- The proxy extracts system prompts from JSON payloads using provider adapters.
- Optimization runs asynchronously and never blocks the proxy request path.
- Response text is captured (when text/JSON) to improve prompt reflection.

