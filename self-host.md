# Self-Hosting Prompt Graft

This guide covers everything you need to run Prompt Graft on your own infrastructure —
from a single laptop to a production deployment. No external services are required beyond
your AI provider and the databases.

---

## What Is Prompt Graft?

Prompt Graft is a **reverse proxy** that sits between your application and any AI API
(OpenAI, Anthropic, Replicate, Bedrock, or any HTTP-based model endpoint). It:

1. **Logs** every request and response (prompt + output).
2. **A/B tests** improved prompt variants by injecting them transparently.
3. **Collects feedback** via a lightweight API that your app calls after each response.
4. **Optimizes** system prompts automatically using an LLM that analyses failure patterns
   and generates improved candidates — no human prompt engineering required.

Your application only needs two changes: point at the proxy URL instead of the AI provider,
and add one header (`X-PG-Session`).

---

## Architecture Overview

```
Your App
   │
   ▼
Proxy  :8080       ← drop-in replacement for your AI provider URL
   │  └── injects optimized prompt variant (if one exists)
   │  └── logs request + response to Redis
   ▼
AI Provider  (Replicate / OpenAI / Bedrock / any HTTP API)
   │
   ▼
Your App  ← receives AI response
   │
   └── POST /feedback/:session  :3001  ← optional: tell Prompt Graft if the response was good
          │
          ▼
       API  :3001  ← stores rating in Redis + Postgres

         (async, every OptimizeEvery)
          ▼
       Optimizer  ← reads rated conversations from Redis
                  ← calls an LLM to analyse failures + generate better prompts
                  ← writes new variants to Redis + Postgres
                  ← janitor retires unused variants after VariantUnusedTTL
```

---

## Prerequisites

- Docker and Docker Compose (v2)
- An AI provider API key (Replicate, OpenAI, Bedrock, etc.)
- A separate API key for the optimizer LLM (used to generate improved prompts)

---

## Quick Start

```bash
# 1. Clone and enter the repository
git clone <repo-url>
cd open-gepa

# 2. Copy the environment template and fill in your keys
cp .env.example .env

# 3. Start everything
docker compose up --build
```

The four services that start:

| Service | Port | Role |
|---|---|---|
| redis | 6379 | Hot cache — active variants, session state, rolling logs |
| db (Postgres) | 5432 | Durable store — variant history, all feedback, prompt templates |
| proxy | 8080 | Intercepts AI requests, injects variants |
| api | 3001 | Receives feedback, serves session/variant info |
| optimizer | — | Periodic prompt optimization + janitor cleanup |

---

## Environment Variables

Copy `.env.example` to `.env` and set the following:

### Required

| Variable | Description |
|---|---|
| `PG_REDIS_URL` | Redis connection, e.g. `redis://redis:6379/0` |
| `PG_DATABASE_URL` | Postgres connection, e.g. `postgres://postgres:password@db:5432/promptgraft?sslmode=disable` |
| `PG_API_KEY_SALT` | Random string used to hash API keys before storing them. Set once and never change. |
| `PG_REPLICATE_API_TOKEN` | API token for the LLM the optimizer uses to generate improved prompts |

### Proxy

| Variable | Default | Description |
|---|---|---|
| `PG_PROXY_ADDR` | `:8080` | Listen address for the proxy |
| `PG_REQUEST_TIMEOUT` | `30s` | Per-request timeout for upstream AI calls |
| `PG_REDIS_TIMEOUT` | `8ms` | Hard cap on Redis variant lookup (never blocks forwarding) |
| `PG_MAX_BUFFER_BYTES` | `524288` | Max body size to buffer for prompt extraction (512 KB) |
| `PG_DEFAULT_UPSTREAM_HOST` | — | Default AI provider host (e.g. `api.replicate.com`) |
| `PG_DEFAULT_UPSTREAM_SCHEME` | `https` | Default upstream scheme |

### API

| Variable | Default | Description |
|---|---|---|
| `PG_API_ADDR` | `:3001` | Listen address for the feedback API |

### Optimizer

| Variable | Default | Description |
|---|---|---|
| `PG_MIN_SAMPLES` | `20` | Minimum rated conversations before a session is optimized |
| `PG_OPTIMIZE_EVERY` | `6h` | Minimum time between optimizer runs for the same session |
| `PG_MAX_VARIANT_AGE` | `168h` | How long a variant stays active in Redis (7 days) |
| `PG_GEPA_OUTPUT_SIZE` | `3` | Number of improved prompt candidates to generate per cycle |
| `PG_GEPA_TOP_N` | `3` | Variants promoted per optimization cycle |
| `PG_GEPA_CROSSOVER` | `true` | Enable crossover in the GEPA candidate pool |
| `PG_OPTIMIZER_LLM_PROVIDER` | `replicate` | LLM provider for the optimizer (`replicate`) |
| `PG_REPLICATE_MODEL` | `openai/gpt-5.4` | Model used to generate improved prompts |
| `PG_REPLICATE_TIMEOUT` | `120s` | Timeout for optimizer LLM calls |

### Janitor

| Variable | Default | Description |
|---|---|---|
| `PG_VARIANT_UNUSED_TTL` | `48h` | Retire variants for sessions with no feedback in this window |
| `PG_JANITOR_INTERVAL` | `1h` | How often the janitor scans for unused variants |

---

## How the Proxy Works

### Sending a Request

Add two headers to your normal AI provider request and point it at the proxy instead:

```bash
curl -X POST http://localhost:8080 \
  -H "Authorization: Bearer $YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -H "X-PG-Session: my-session-1" \
  -H "X-PG-Upstream-Url: https://api.replicate.com/v1/models/google/gemini-3.1-pro/predictions" \
  -H "X-PG-Api-Style: replicate" \
  -H "Prefer: wait" \
  -d '{"input": {"prompt": "You are a helpful assistant. Answer: why do stars twinkle?"}}'
```

The proxy:
1. Reads your API key from `Authorization` and hashes it — the raw key is never stored.
2. Looks up the session in Redis for an active variant (takes ≤8ms; never blocks the forward).
3. If a variant exists, replaces the system prompt in the body transparently.
4. Forwards the request to the upstream URL.
5. Streams the response back to you with three extra headers:

```
X-PG-Session-Id:      my-session-1
X-PG-Conversation-Id: a3f8c1d2e4b5...   ← stable hash of this prompt
X-PG-Variant-Id:      7e9a2b1c...        ← which variant was used (if any)
```

### Provider Styles

The `X-PG-Api-Style` header tells the proxy where to find the system prompt in the request
body and where to extract the response text. Available styles:

| Style | Where prompt lives | Where response text lives |
|---|---|---|
| `replicate` | `input.prompt` | `output` (string or array) |
| `openai-chat` | `messages[0].content` | `choices[].message.content` joined |
| `generic` | (no extraction) | (no extraction) |

If you omit the style header, the proxy falls back to host-based detection, then `generic`.

---

## How Sessions Work

A **session** is a logical grouping of related conversations — typically one per product
feature, user cohort, or agent workflow. You control the session name via `X-PG-Session`.

All conversations in a session share:
- The same prompt template (inferred automatically from their common prefix).
- The same pool of A/B test variants.
- The same feedback counters.

Two prompts that differ only in dynamic tokens (UUIDs, dates, URLs, numbers) are considered
the same template and grouped together automatically.

---

## How Feedback Works

After your application receives a response, post a rating (1 = good, -1 = bad):

```bash
curl -X POST http://localhost:3001/feedback/my-session-1 \
  -H "Authorization: Bearer $YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -H "X-PG-Conversation-Id: a3f8c1d2e4b5..." \
  -H "X-PG-Variant-Id: 7e9a2b1c..." \
  -d '{"rating": 1}'
```

Pass the `X-PG-Conversation-Id` and `X-PG-Variant-Id` values from the proxy response headers
so the feedback is attributed to the exact conversation and variant that produced it.

**What happens with the feedback:**
- Immediately written to Redis (fast scan counters for the optimizer).
- Immediately written to Postgres (`feedback_events` table, permanent record).
- Aggregated by the optimizer at the next cycle to score prompt candidates.

---

## How Optimization Works

The optimizer runs on a schedule (default: every 6 hours). For each session that has
accumulated at least `PG_MIN_SAMPLES` rated conversations:

1. **Load samples** — balanced selection of positive, negative, and neutral conversations
   from Redis.
2. **Analyse** — calls the optimizer LLM with the samples and scores. The LLM returns a
   diagnosis: what worked, what failed, and why.
3. **Generate** — calls the LLM again, asking it to produce `PG_GEPA_OUTPUT_SIZE` improved
   system prompts based on the diagnosis.
4. **Promote** — writes the new prompts as equal-weight variants to both Redis and Postgres.
   The old variants are retired in Postgres.
5. **A/B test** — the proxy begins serving the new variants immediately. Weighted random
   selection means each variant gets equal traffic.

The cycle then repeats: more feedback → better analysis → better prompts.

---

## Data Persistence and Failover

Prompt Graft uses a two-tier storage architecture:

### Redis (hot cache)
- Stores active variants, rolling request logs (last 100 per session), and feedback counters.
- Fast: the proxy reads variants with an 8ms hard cap.
- Ephemeral: data expires via TTL. If Redis restarts, variants refill from Postgres
  automatically on the first request (cache-miss path).

### Postgres (durable store)
- Stores every feedback event ever recorded, the full variant history, prompt templates,
  and best prompts.
- Survives restarts, crashes, and failovers.
- The migration SQL runs idempotently at startup — no manual schema management needed.

### What happens on Redis restart

The proxy continues to work. On the first request for each session:
1. Redis returns a cache miss.
2. The layered store queries Postgres for active variants.
3. Variants are re-warmed into Redis for the remaining active duration.
4. Subsequent requests are served from cache again.

No optimization progress is lost. No variants are lost.

### What happens on Postgres restart

The proxy and API continue to work normally — they use Redis exclusively on the hot path.
Feedback written during the outage is stored only in Redis (the Postgres write fails and is
logged). When Postgres recovers, new feedback writes resume. Historical data in Redis will
eventually be evicted by TTL, but the most recent feedback is durable in Postgres once it
reconnects.

### What happens if Postgres is never configured

Set `PG_DATABASE_URL` to nothing (or omit it). All three services start and run correctly.
Variants expire via Redis TTL. No janitor runs. Suitable for development and evaluation.

---

## How the Janitor Works

The janitor is a background goroutine inside the optimizer binary. It runs every
`PG_JANITOR_INTERVAL` (default: 1 hour) and:

1. Queries Postgres for sessions that have **active variants** but **no feedback events**
   in the last `PG_VARIANT_UNUSED_TTL` window (default: 48 hours).
2. Marks those variants as `retired_at = now()` in Postgres.
3. Deletes the Redis cache key for each retired session.

This prevents stale variants accumulating for sessions that were tested and abandoned.
Active sessions (any feedback in the last 48h) are never touched.

The janitor only starts when `PG_DATABASE_URL` is set. It does not affect Redis-only
deployments.

---

## Inspecting State

### Session summary

```bash
curl http://localhost:3001/sessions/my-session-1 \
  -H "Authorization: Bearer $YOUR_API_KEY"
```

Returns the latest prompt snippet, active variant, and feedback totals (up/down).

### Variant and history

```bash
curl http://localhost:3001/variants/my-session-1 \
  -H "Authorization: Bearer $YOUR_API_KEY"
```

Returns current active variants, the best prompt found so far (with score), and the full
optimization history.

---

## Running Without Docker

```bash
# Start Redis and Postgres however you prefer, then:

export PG_REDIS_URL=redis://localhost:6379/0
export PG_DATABASE_URL=postgres://postgres:password@localhost:5432/promptgraft?sslmode=disable
export PG_API_KEY_SALT=your-random-salt
export PG_REPLICATE_API_TOKEN=your-token

# In three terminals:
go run ./cmd/proxy
go run ./cmd/api
go run ./cmd/optimizer
```

---

## Production Checklist

- [ ] Set `PG_API_KEY_SALT` to a long random string and never change it (changing it
      invalidates all existing hashed keys in the store).
- [ ] Set `PG_LOG_LEVEL=info` (debug is very verbose).
- [ ] Increase `PG_MIN_SAMPLES` (default 20) to match your traffic — optimize only when
      you have enough signal.
- [ ] Set `PG_OPTIMIZE_EVERY` to match your release cadence (e.g. `24h`).
- [ ] Configure Postgres with a persistent volume in docker-compose or your orchestrator.
- [ ] Configure Redis with AOF or RDB persistence if you want to survive Redis restarts
      without Postgres cold-start latency.
- [ ] Set resource limits on the optimizer container — LLM calls during optimization can
      be slow and bursty, but the optimizer never touches the proxy hot path.
- [ ] Run the proxy behind a load balancer if you need horizontal scale — it is stateless
      (all state lives in Redis/Postgres).
