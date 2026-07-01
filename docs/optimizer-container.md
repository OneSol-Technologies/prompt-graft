# Optimizer Container

The optimizer (`cmd/optimizer/main.go`) is an **offline, async, cron-like process** that
analyses real user feedback, generates improved prompt variants via LLM, and promotes them
into the store. It never runs in the hot path — the proxy ignores it entirely.

---

## Entry Point

`cmd/optimizer/main.go`:

1. Initialises Redis, optionally Postgres, and a layered store.
2. Creates a `gepa.LLMClient` (defaults to Replicate; falls back to `NoopClient` if no API key).
3. Creates the `Driver` (the main optimization loop).
4. If Postgres is available, starts the **janitor** in a background goroutine.
5. Enters an infinite loop: `driver.RunOnce()` → sleep `OptimizeEvery` → repeat.

The only way the container exits is `SIGINT` or `SIGTERM`.

---

## Timing

### Main loop interval

The **optimization cycle** runs every `PG_OPTIMIZE_EVERY` (default **6 hours**) per session.
The outer `for` loop in `main()` runs `RunOnce` then sleeps `OptimizeEvery` before the next
scan. Each `RunOnce` call first queries the store for sessions that:

- have **≥ `PG_MIN_SAMPLES`** (default 20) rated conversations, **and**
- have not been optimised within the last `OptimizeEvery` window (tracked by
  `session_feedback.lastOptimized`).

### LLM calls

Each candidate generation involves **two sequential LLM calls** per session:

1. **Analysis** (`llm.Complete` + `buildAnalysisPrompt`) — the LLM diagnoses what worked/
   failed across the sample set.
2. **Generation** (`llm.Complete` + `buildGenerationPrompt`) — the LLM produces
   `PG_GEPA_OUTPUT_SIZE` (default 3) candidate system prompts.

Each LLM call has a **120s timeout** (Replicate polling; `PG_REPLICATE_TIMEOUT`).

### Janitor interval

The janitor runs on a Ticker at `PG_JANITOR_INTERVAL` (default **1 hour**) and performs:

1. **Copy logs** from Redis to Postgres (`conversation_logs`).
2. **Retire unused variants** — sessions with active variants but no feedback event within
   `PG_VARIANT_UNUSED_TTL` (default **48 hours**) get their Redis variant keys evicted.

### GEPA rollout budget

When the full GEPA loop (`gepa.Optimizer.Run()`) is used, it runs up to
`PG_GEPA_ROLLOUT_BUDGET` (default **50**) iterations per invocation, each involving:

- A Pareto-frontier computation over the pool
- An LLM reflection call on a minibatch of `PG_GEPA_MINIBATCH_SIZE` (default 5)
- An LLM mutation or crossover call
- A scoring call

---

## Modules

### Driver (`internal/optimizer/driver.go`)

**Purpose:** Orchestrates one end-to-end optimization cycle per session.

```go
func (d *Driver) RunOnce(ctx context.Context)
```

**Sequence per session:**

1. `store.ReadySessions()` — finds sessions ready for optimisation.
2. `store.LoadConversationSamples()` — loads a balanced sample set (positive / negative /
   neutral).
3. `deriveTemplateFromSamples()` — extracts the common prefix of all sample prompts as the
   session's inferred template.
4. `allPositive()` short-circuit — if every sample is rated +1, the system prompt is already
   optimal; mark optimised and skip generation.
5. `buildAnalysisPrompt()` + first LLM call — the LLM diagnoses failure patterns.
6. `buildGenerationPrompt()` + second LLM call — the LLM produces `PG_GEPA_OUTPUT_SIZE`
   candidate prompts.
7. `parseCandidates()` — parses JSON array or newline-separated fallback from LLM output.
8. `RollupConversationFeedback()` — aggregates per-conversation feedback into per-variant
   totals.
9. `Promoter.Promote()` — writes variants and best-prompt records to the store.
10. `MarkSessionOptimized()` — records `lastOptimized` timestamp so the session is not
    reprocessed until the next `OptimizeEvery` window.

---

### Promoter (`internal/optimizer/promoter.go`)

**Purpose:** Writes optimised variant candidates into the layered store.

```go
func (p *Promoter) Promote(ctx context.Context, keyHash, sessionID string, result *gepa.Result) error
```

- Generates a deterministic variant `ID` via `sha256(prompt)[:8]`.
- Assigns equal weight (`1.0 / N`) to all candidates.
- Calls `store.SetVariants()` (Redis + Postgres).
- Updates `best_prompt` and appends to `prompt_history`.

---

### Dataset Loader (`internal/optimizer/dataset.go`)

**Purpose:** Simple adapter that loads a `gepa.Dataset` from the store for the full GEPA rollout loop.

```go
func (d *DatasetLoader) Load(ctx context.Context, keyHash, groupID string) (gepa.Dataset, error)
```

Wraps `store.LoadDataset()`.

---

### Janitor (`internal/optimizer/janitor/janitor.go`)

**Purpose:** Background goroutine that keeps the store clean.

**Notable:** Only runs when Postgres is available. Without Postgres, Redis TTL is the only
cleanup mechanism.

**Two responsibilities:**

1. **copyLogs** — scans all Redis log keys, reads up to 200 entries per session, and inserts
   them into Postgres `conversation_logs` (`INSERT … ON CONFLICT DO NOTHING` for idempotency).
2. **retireUnused** — queries `pg.FindUnusedVariantSessions()` for sessions whose
   `feedback_events` table has no rows within the `VariantUnusedTTL` window, then evicts the
   Redis variant key.

**Note:** The Postgres `RetireVariants` call is currently **commented out** (janitor.go:123-126).
Only Redis eviction runs.

---

### GEPA Package (`internal/optimizer/gepa/`)

A general-purpose evolutionary prompt optimisation framework. Used by the Driver for
iterative refinement when enough data is available.

#### `gepa.go` — GEPA config, Optimizer, and Run loop

```go
func (o *Optimizer) Run(ctx context.Context, seedPrompt string, dataset Dataset) (*Result, error)
```

Config:

| Field | Default | Env var |
|---|---|---|
| `RolloutBudget` | 50 | `PG_GEPA_ROLLOUT_BUDGET` |
| `MinibatchSize` | 5 | `PG_GEPA_MINIBATCH_SIZE` |
| `ParetoFraction` | 0.3 | — |
| `MinDelta` | 0.01 | — |
| `CrossoverEnabled` | true | `PG_GEPA_CROSSOVER` |
| `CrossoverFrequency` | 0.2 | — |
| `TopN` | 3 | `PG_GEPA_TOP_N` |

**Run loop:**

1. Split dataset into Pareto set + feedback set via `Dataset.Split()`.
2. Score the `seedPrompt` against the Pareto set (baseline).
3. For each iteration (`RolloutBudget`):
   a. Compute `ParetoFrontier` — non-dominated candidates.
   b. `SampleFromFrontier` — frequency-weighted parent selection.
   c. Sample a minibatch from the feedback set.
   d. `reflector.Reflect()` — LLM analyses parent + minibatch → ASI + suggestions.
   e. `reflector.Mutate()` (or `Crossover()` if enabled and random threshold hit) —
      LLM produces a child prompt.
   f. Score child; accept only if `childScore > parentScore + MinDelta`.
4. Return top-N candidates + best.

#### `candidate.go` — Candidate and CandidatePool

- `Candidate`: holds prompt, per-example scores, aggregate score, parent ID, generation
  number, and ASI (diagnosis).
- `CandidatePool`: thread-safe (sync.RWMutex) slice. Methods: `Add`, `All`, `Best`.

#### `pareto.go` — Multi-objective Pareto frontier

- `ParetoFrontier()`: returns non-dominated subset. C dominates D if all its scores are ≥
  and at least one is strictly >.
- `SampleFromFrontier()`: frequency-weighted sampling — candidates seen more often on the
  frontier have higher probability.

#### `reflect.go` — LLM-guided reflection, mutation, crossover

- `Reflect()`: Formats a minibatch of examples with scores into a prompt → returns
  `ReflectionResult{ASI, Suggestions}`.
- `Mutate()`: Rewrites the parent prompt using the ASI diagnosis.
- `Crossover()`: Merges two Pareto-frontier prompts into one.
- All three skip their LLM call when the client is `nil`, returning the parent prompt
  unchanged.

#### `scorer.go` — Scoring functions

- `FeedbackScorer`: maps rating (+1 → 1.0, 0 → 0.5, -1 → 0.0). For unseen data, falls back
  to token-overlap similarity.
- `ExactMatchScorer`: 1.0 if expected string is contained in output, 0.0 otherwise.
- `similarityScore`: Jaccard-like token overlap between prompt and input text.

#### `dataset.go` — DataPoint and Dataset

- `DataPoint`: `Input`, `Output`, `Rating`, `ASI`, `VariantID`, `Comment`.
- `Dataset.Split(paretoFraction, seed)`: shuffles deterministically and splits into a Pareto
  training set and a feedback minibatch set.
- `Dataset.Minibatch(n, rng)`: random subsample.

#### `llm.go` — LLM client abstraction

- `LLMClient` interface: `Complete(ctx, systemPrompt, userPrompt) (string, error)`.
- `ReplicateClient`: full implementation using Replicate's predictions API (sync create +
  polling fallback).
- `OpenAIClient` / `AnthropicClient`: stubs returning "not implemented".
- `NoopClient`: returns `""`; used when no API key is available.
- `NewLLMClient(cfg)`: factory — Replicate if provider is `replicate`, else NoopClient if
  no API key, else stub.

---

## Interaction diagram

```
┌──────────────────────────────────────────────────────────┐
│ cmd/optimizer/main.go                                    │
│                                                          │
│  for {                                                    │
│    driver.RunOnce(ctx)          ◄── every OptimizeEvery   │
│    time.After(OptimizeEvery)                              │
│  }                                                        │
│                                                          │
│  go janitor.Run(ctx)           ◄── every JanitorInterval  │
│    (only when Postgres present)                           │
└──────────────────────────────────────────────────────────┘

driver.RunOnce per session:
  ── ReadySessions          ── scan for ≥MinSamples, not recently optimised
  ── LoadConversationSamples ── balanced sample set
  ── deriveTemplate         ── common prefix as prompt template
  ── [LLM] analysis call    ── diagnose failures
  ── [LLM] generation call  ── produce N candidate prompts
  ── parseCandidates        ── JSON array / newline fallback
  ── RollupConversationFeedback ── aggregate scores
  ── Promoter.Promote       ── write variants + best prompt
  ── MarkSessionOptimized   ── record timestamp
```

## Key invariants

- **Never blocks the proxy.** The optimizer runs in its own container on its own schedule.
- **Postgres is optional.** Without Postgres, the optimizer still runs (Redis-only mode).
- **The janitor only runs with Postgres.** Redis TTL expires variants eventually.
- **Postgres `RetireVariants` is currently disabled.** Only Redis variant eviction happens
  on janitor cycles.
