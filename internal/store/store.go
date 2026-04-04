package store

import (
	"context"
	"time"

	"promptguru/internal/optimizer/gepa"
)

type Variant struct {
	ID           string  `json:"id"`
	SystemPrompt string  `json:"systemPrompt"`
	Weight       float64 `json:"weight"`
}

type VariantSet struct {
	Variants    []Variant `json:"variants"`
	ActiveUntil int64     `json:"activeUntil"`
}

type BestPrompt struct {
	Prompt     string  `json:"prompt"`
	Score      float64 `json:"score"`
	PromotedAt int64   `json:"promotedAt"`
}

type HistoryEntry struct {
	Prompt     string  `json:"prompt"`
	Score      float64 `json:"score"`
	PromotedAt int64   `json:"promotedAt"`
	RetiredAt  int64   `json:"retiredAt"`
}

type FeedbackSummary struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type ConversationFeedback struct {
	ConversationID string
	VariantID      string
	Score          int
	Prompt         string
	Response       string
}

type SessionInfo struct {
	SessionID       string          `json:"sessionId"`
	VariantID       string          `json:"variantId"`
	ConversationID  string          `json:"conversationId"`
	PromptSnippet   string          `json:"promptSnippet"`
	FeedbackSummary FeedbackSummary `json:"feedbackSummary"`
}

type VariantsInfo struct {
	SessionID  string         `json:"sessionId"`
	Variants   []Variant      `json:"variants"`
	BestPrompt *BestPrompt    `json:"bestPrompt,omitempty"`
	History    []HistoryEntry `json:"history"`
}

type SessionRef struct {
	KeyHash   string
	SessionID string
}

type ConversationLog struct {
	KeyHash        string
	SessionID      string
	ConversationID string
	VariantID      string
	Prompt         string
	ResponseText   string
}

type Store interface {
	GetVariant(ctx context.Context, keyHash, sessionID string) (*VariantSet, error)
	SetVariants(ctx context.Context, keyHash, sessionID string, variants []Variant, ttl time.Duration) error
	GetSessionPrompt(ctx context.Context, keyHash, sessionID string) (string, error)
	SetSessionPrompt(ctx context.Context, keyHash, sessionID, prompt string, ttl time.Duration) error
	LogRequest(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, promptSnippet, prompt, promptOriginal string, body []byte) error
	LogResponse(ctx context.Context, keyHash, sessionID, variantID, conversationID, contentType, responseText string, body []byte) error

	RecordFeedback(ctx context.Context, keyHash, sessionID, conversationID, variantID string, rating int) error
	GetSessionInfo(ctx context.Context, keyHash, sessionID string) (*SessionInfo, error)
	GetVariantsInfo(ctx context.Context, keyHash, sessionID string) (*VariantsInfo, error)
	GetVariantFeedback(ctx context.Context, keyHash, sessionID, variantID string) (FeedbackSummary, error)
	GetSessionFeedback(ctx context.Context, keyHash, sessionID string) (FeedbackSummary, error)

	ReadySessions(ctx context.Context, minSamples int, optimizeEvery time.Duration) ([]SessionRef, error)
	LoadDataset(ctx context.Context, keyHash, sessionID string) (gepa.Dataset, error)
	UpdateBestPrompt(ctx context.Context, keyHash, sessionID string, best BestPrompt) error
	AppendHistory(ctx context.Context, keyHash, sessionID string, entry HistoryEntry) error
	MarkSessionOptimized(ctx context.Context, keyHash, sessionID string) error
	MarkFeedbackUsed(ctx context.Context, keyHash, sessionID string, conversationIDs []string) error

	LoadConversationSamples(ctx context.Context, keyHash, sessionID string, perVariant int) ([]ConversationFeedback, error)
	RollupConversationFeedback(ctx context.Context, keyHash, sessionID string) error
}
