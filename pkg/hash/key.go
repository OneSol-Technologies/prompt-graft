package hash

import (
    "crypto/sha256"
    "encoding/hex"
)

func APIKey(salt, key string) string {
    h := sha256.Sum256([]byte(salt + key))
    return hex.EncodeToString(h[:8])
}
