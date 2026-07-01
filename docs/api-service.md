# API Service

The API (`cmd/api/main.go`) is a lightweight HTTP server that exposes the feedback loop,
session introspection, and variant management endpoints. It shares the same store layer
(Redis + optional Postgres) as the proxy and optimizer.

---

## Entry Point

`cmd/api/main.go`:

1. Loads config and initialises the logger.
2. Initialises a layered store (Redis required; Postgres optional).
3. Calls `api.NewServer(cfg, store, log)`.
4. Starts the HTTP server and blocks on `SIGINT`/`SIGTERM` for graceful shutdown (5s timeout).

---

## Server (`internal/api/server.go`)

**Purpose:** Wires the router, middleware, and auth into a standard `http.Server`.

```go
func NewServer(cfg *config.Config, st store.Store, log *logging.Logger) *http.Server
```

### Auth chain

Three tiers of access control, evaluated in order:

| Path | Auth mechanism | Config key |
|---|---|---|
| `/health` | Public (no auth) | — |
| `/`, `/studio-chat.html` | BasicAuth (user `admin`, password from config) | `PG_STUDIO_PASSWORD` |
| `/feedback/`, `/sessions/`, `/variants/` | Bearer token | `PG_AUTH_TOKEN` |

- If `PG_STUDIO_PASSWORD` is empty, the studio UI is public.
- If `PG_AUTH_TOKEN` is empty, all API routes are public.
- Unknown paths (404) are also protected by Bearer token.

### Middleware

The server wraps the auth handler with `middleware.CORS`:

- `Access-Control-Allow-Origin: *`
- Allows `Authorization`, `Content-Type`, and all `X-PG-*` headers.
- Exposes `X-PG-Session-Id`, `X-PG-Conversation-Id`, `X-PG-Variant-Id`.
- Preflight `OPTIONS` requests return 204 immediately.

---

## Routes

All routes are registered in `internal/api/routes/register.go` via a shared `Handler`
struct that holds references to the config, store, and logger.

```
Handler {
    cfg   *config.Config
    store store.Store
    log   *logging.Logger
}
```

### `GET /health`

Returns `{"service":"api","status":"ok"}`. Unauthenticated — used by health checks and
container orchestrators.

### `POST /feedback/:sessionId`

Record user feedback for a conversation.

**Request body:**

```json
{"rating": 1, "comment": "optional rationale"}
```

- `rating`: `1` (positive) or `-1` (negative).
- `comment`: optional free-text rationale, enriches the optimizer's LLM analysis.

**Headers:**

| Header | Required | Description |
|---|---|---|
| `Authorization` | Yes | Bearer token (or API key, hashed with `PG_API_KEY_SALT`) |
| `X-PG-Variant-Id` | No | Identifies which variant served the conversation |
| `X-PG-Conversation-Id` | No | Identifies the conversation; inferred from session if absent |

**Behaviour:**

1. Derives `keyHash` from `Authorization` header via `hash.APIKey(salt, auth)`.
2. If `X-PG-Conversation-Id` is empty, calls `store.GetSessionInfo()` to fill it (and
   `variantID` if missing).
3. Calls `store.RecordFeedback()` — writes to Redis (scan counters) + Postgres
   (append-only `feedback_events` row).
4. Returns `{"ok": true}`.

**Response:** `200 OK` or `400`/`500` on error.

### `GET /sessions/:sessionId`

Return metadata about a session.

**Headers:**

| Header | Required | Description |
|---|---|---|
| `Authorization` | Yes | Bearer token or API key |

**Response `200 OK`:**

```json
{
  "sessionId":      "...",
  "variantId":      "...",
  "conversationId": "...",
  "promptSnippet":  "...",
  "feedbackSummary": {"up": 12, "down": 3}
}
```

Backed by `store.GetSessionInfo()`.

### `GET /variants/:sessionId`

Return the current variant set, best prompt, and history for a session.

**Headers:**

| Header | Required | Description |
|---|---|---|
| `Authorization` | Yes | Bearer token or API key |

**Response `200 OK`:**

```json
{
  "sessionId":  "...",
  "variants":   [{"id":"...","systemPrompt":"...","weight":0.5}],
  "bestPrompt": {"prompt":"...","score":0.87,"promotedAt":1712345678},
  "history":    [{"prompt":"...","score":0.87,"promotedAt":...,"retiredAt":...}]
}
```

Backed by `store.GetVariantsInfo()` — reads from Redis (warmed from Postgres on cache miss).

### `GET /` and `GET /studio-chat.html`

Serves the static studio chat UI. Protected by BasicAuth when `PG_STUDIO_PASSWORD` is set.

---

## Helper (`internal/api/routes/helpers.go`)

```go
func writeJSON(w http.ResponseWriter, payload any)
```

Sets `Content-Type: application/json` and JSON-encodes the payload. Swallows encode errors
(returns partially written response on failure).

---

## Interaction diagram

```
Client
  │
  ├─ POST /feedback/:sessionId ──────► routes.handleFeedback
  │                                      │ hash.APIKey(auth)
  │                                      │ store.RecordFeedback(keyHash, session, ...)
  │                                      │   ├── Redis: update scan counters
  │                                      │   └── Postgres: INSERT feedback_events
  │                                      ▼
  │                                   {"ok": true}
  │
  ├─ GET /sessions/:sessionId ───────► routes.handleSessions
  │                                      │ hash.APIKey(auth)
  │                                      │ store.GetSessionInfo(keyHash, session)
  │                                      ▼
  │                                   session info JSON
  │
  └─ GET /variants/:sessionId ───────► routes.handleVariants
                                         │ hash.APIKey(auth)
                                         │ store.GetVariantsInfo(keyHash, session)
                                         ▼
                                      variants + best prompt + history JSON
```

## Key invariants

- **No Redis or Postgres dependency in the hot path of feedback recording.** Redis writes
  complete first; Postgres failures are logged and swallowed.
- **Auth is optional.** When `PG_AUTH_TOKEN` and `PG_STUDIO_PASSWORD` are both empty, all
  routes are public.
- **`X-PG-Conversation-Id` is auto-inferred** from session state if not explicitly provided
  in the feedback request.
- **The studio UI is a single static HTML file** (`studio-chat.html` in the repo root). No
  build step, no framework.
