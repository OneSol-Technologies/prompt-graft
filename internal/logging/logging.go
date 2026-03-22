package logging

import (
    "log"
    "os"
    "strings"
    "sync"
)

type Level int

const (
    LevelDebug Level = iota
    LevelInfo
    LevelWarn
    LevelError
)

type Logger struct {
    mu    sync.RWMutex
    level Level
    base  *log.Logger
}

func New() *Logger {
    l := &Logger{base: log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)}
    l.SetLevel(os.Getenv("PG_LOG_LEVEL"))
    return l
}

func (l *Logger) SetLevel(raw string) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.level = parseLevel(raw)
}

func (l *Logger) Debugf(format string, args ...any) {
    if l.enabled(LevelDebug) {
        l.base.Printf("DEBUG "+format, args...)
    }
}

func (l *Logger) Infof(format string, args ...any) {
    if l.enabled(LevelInfo) {
        l.base.Printf("INFO  "+format, args...)
    }
}

func (l *Logger) Warnf(format string, args ...any) {
    if l.enabled(LevelWarn) {
        l.base.Printf("WARN  "+format, args...)
    }
}

func (l *Logger) Errorf(format string, args ...any) {
    if l.enabled(LevelError) {
        l.base.Printf("ERROR "+format, args...)
    }
}

func (l *Logger) enabled(level Level) bool {
    l.mu.RLock()
    defer l.mu.RUnlock()
    return level >= l.level
}

func parseLevel(raw string) Level {
    switch strings.ToLower(strings.TrimSpace(raw)) {
    case "debug", "trace", "":
        return LevelDebug
    case "info":
        return LevelInfo
    case "warn", "warning":
        return LevelWarn
    case "error":
        return LevelError
    default:
        return LevelDebug
    }
}
