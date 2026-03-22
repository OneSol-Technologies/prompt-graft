package store

import "fmt"

func KeyVariants(keyHash, sessionID string) string {
    return fmt.Sprintf("pg:variants:%s:%s", keyHash, sessionID)
}

func KeyLog(keyHash, sessionID string) string {
    return fmt.Sprintf("pg:log:%s:%s", keyHash, sessionID)
}

func KeyFeedback(keyHash, sessionID, variantID string) string {
    return fmt.Sprintf("pg:feedback:%s:%s:%s", keyHash, sessionID, variantID)
}

func KeySessionFeedback(keyHash, sessionID string) string {
    return fmt.Sprintf("pg:session_feedback:%s:%s", keyHash, sessionID)
}

func KeyConversationFeedback(keyHash, sessionID, conversationID string) string {
    return fmt.Sprintf("pg:conversation_feedback:%s:%s:%s", keyHash, sessionID, conversationID)
}

func KeySessionPrompt(keyHash, sessionID string) string {
    return fmt.Sprintf("pg:session_prompt:%s:%s", keyHash, sessionID)
}

func KeyBestPrompt(keyHash, sessionID string) string {
    return fmt.Sprintf("pg:best_prompt:%s:%s", keyHash, sessionID)
}

func KeyHistory(keyHash, sessionID string) string {
    return fmt.Sprintf("pg:history:%s:%s", keyHash, sessionID)
}
