package body

import (
    "bytes"
    "io"
    "strings"
)

func StreamOrBuffer(contentType string, body io.Reader, maxBytes int) ([]byte, io.Reader, error) {
    ct := strings.ToLower(strings.TrimSpace(contentType))
    if strings.HasPrefix(ct, "multipart/") ||
        strings.HasPrefix(ct, "application/octet-stream") ||
        strings.HasPrefix(ct, "audio/") ||
        strings.HasPrefix(ct, "video/") ||
        strings.HasPrefix(ct, "image/") {
        return nil, body, nil
    }

    limited := &io.LimitedReader{R: body, N: int64(maxBytes + 1)}
    buf, err := io.ReadAll(limited)
    if err != nil {
        return nil, nil, err
    }
    if len(buf) > maxBytes {
        return nil, io.MultiReader(bytes.NewReader(buf), body), nil
    }
    return buf, nil, nil
}
