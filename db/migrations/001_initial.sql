-- Prompt Graft: initial schema
-- All tables are idempotent (CREATE TABLE IF NOT EXISTS).
-- Run at startup in every service that connects to Postgres.

-- Active and retired prompt variants per session.
-- Retired when superseded by a new optimization cycle or by the janitor.
CREATE TABLE IF NOT EXISTS variants (
    id            TEXT                NOT NULL,
    key_hash      TEXT                NOT NULL,
    session_id    TEXT                NOT NULL,
    system_prompt TEXT                NOT NULL,
    weight        DOUBLE PRECISION    NOT NULL DEFAULT 1.0,
    active_until  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ         NOT NULL DEFAULT now(),
    retired_at    TIMESTAMPTZ,
    PRIMARY KEY (key_hash, session_id, id)
);
CREATE INDEX IF NOT EXISTS idx_variants_active
    ON variants (key_hash, session_id)
    WHERE retired_at IS NULL;

-- Every feedback event ever recorded.  Never deleted; used by the janitor
-- to determine whether a session has been active recently.
CREATE TABLE IF NOT EXISTS feedback_events (
    id              BIGSERIAL   PRIMARY KEY,
    key_hash        TEXT        NOT NULL,
    session_id      TEXT        NOT NULL,
    conversation_id TEXT        NOT NULL,
    variant_id      TEXT,
    rating          INT         NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_feedback_session
    ON feedback_events (key_hash, session_id);
CREATE INDEX IF NOT EXISTS idx_feedback_created
    ON feedback_events (created_at);

-- Inferred prompt template per session (common prefix of all seen prompts).
CREATE TABLE IF NOT EXISTS session_prompts (
    key_hash    TEXT        NOT NULL,
    session_id  TEXT        NOT NULL,
    prompt      TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (key_hash, session_id)
);

-- Best prompt currently promoted for each session.
CREATE TABLE IF NOT EXISTS best_prompts (
    key_hash    TEXT             NOT NULL,
    session_id  TEXT             NOT NULL,
    prompt      TEXT             NOT NULL,
    score       DOUBLE PRECISION NOT NULL DEFAULT 0,
    promoted_at TIMESTAMPTZ      NOT NULL,
    PRIMARY KEY (key_hash, session_id)
);

-- Full optimization history (all promoted prompts, newest first).
CREATE TABLE IF NOT EXISTS prompt_history (
    id          BIGSERIAL        PRIMARY KEY,
    key_hash    TEXT             NOT NULL,
    session_id  TEXT             NOT NULL,
    prompt      TEXT             NOT NULL,
    score       DOUBLE PRECISION NOT NULL DEFAULT 0,
    promoted_at TIMESTAMPTZ      NOT NULL,
    retired_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_history_session
    ON prompt_history (key_hash, session_id, promoted_at DESC);

-- Conversation logs copied from Redis by the janitor.
-- One row per (session, conversation); UNIQUE prevents double-insertion.
CREATE TABLE IF NOT EXISTS conversation_logs (
    id              BIGSERIAL   PRIMARY KEY,
    key_hash        TEXT        NOT NULL,
    session_id      TEXT        NOT NULL,
    conversation_id TEXT        NOT NULL,
    variant_id      TEXT,
    prompt          TEXT,
    response_text   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (key_hash, session_id, conversation_id)
);
CREATE INDEX IF NOT EXISTS idx_convlogs_session
    ON conversation_logs (key_hash, session_id);

-- Per-session optimizer state (last optimized timestamp).
CREATE TABLE IF NOT EXISTS session_metadata (
    key_hash       TEXT        NOT NULL,
    session_id     TEXT        NOT NULL,
    last_optimized TIMESTAMPTZ,
    PRIMARY KEY (key_hash, session_id)
);
