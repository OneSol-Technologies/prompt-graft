package config

import (
    "os"
    "strconv"
    "time"
)

type Config struct {
    ProxyAddr      string
    RequestTimeout time.Duration
    RedisTimeout   time.Duration
    MaxBufferBytes int

    RedisURL string

    MinSamples        int
    OptimizeEvery     time.Duration
    MaxVariantAge     time.Duration
    GEPARolloutBudget int
    GEPAMinibatchSize int
    GEPATopN          int
    GEPAOutputSize    int
    GEPACrossover     bool

    OptimizerLLMProvider string
    OptimizerLLMModel    string
    OptimizerLLMAPIKey   string

    ReplicateAPIToken          string
    ReplicateBaseURL           string
    ReplicateModel             string
    ReplicateReasoningEffort   string
    ReplicateVerbosity         string
    ReplicateMaxCompletionTokens int
    ReplicateTemperature       float64
    ReplicateTopP              float64
    ReplicatePresencePenalty   float64
    ReplicateFrequencyPenalty  float64
    ReplicateTimeout           time.Duration

    APIAddr string

    APIKeySalt string

    DefaultUpstreamHost   string
    DefaultUpstreamScheme string
}

func Load() *Config {
    return &Config{
        ProxyAddr:      getEnv("PG_PROXY_ADDR", ":8080"),
        RequestTimeout: getDuration("PG_REQUEST_TIMEOUT", 30*time.Second),
        RedisTimeout:   getDuration("PG_REDIS_TIMEOUT", 8*time.Millisecond),
        MaxBufferBytes: getInt("PG_MAX_BUFFER_BYTES", 524288),

        RedisURL: getEnv("PG_REDIS_URL", ""),

        MinSamples:        getInt("PG_MIN_SAMPLES", 20),
        OptimizeEvery:     getDuration("PG_OPTIMIZE_EVERY", 6*time.Hour),
        MaxVariantAge:     getDuration("PG_MAX_VARIANT_AGE", 7*24*time.Hour),
        GEPARolloutBudget: getInt("PG_GEPA_ROLLOUT_BUDGET", 50),
        GEPAMinibatchSize: getInt("PG_GEPA_MINIBATCH_SIZE", 5),
        GEPATopN:          getInt("PG_GEPA_TOP_N", 3),
        GEPAOutputSize:    getInt("PG_GEPA_OUTPUT_SIZE", 3),
        GEPACrossover:     getBool("PG_GEPA_CROSSOVER", true),

        OptimizerLLMProvider: getEnv("PG_OPTIMIZER_LLM_PROVIDER", "replicate"),
        OptimizerLLMModel:    getEnv("PG_OPTIMIZER_LLM_MODEL", "openai/gpt-5.4"),
        OptimizerLLMAPIKey:   getEnv("PG_OPTIMIZER_LLM_API_KEY", ""),

        ReplicateAPIToken:            getEnv("PG_REPLICATE_API_TOKEN", ""),
        ReplicateBaseURL:             getEnv("PG_REPLICATE_BASE_URL", "https://api.replicate.com"),
        ReplicateModel:               getEnv("PG_REPLICATE_MODEL", "openai/gpt-5.4"),
        ReplicateReasoningEffort:     getEnv("PG_REPLICATE_REASONING_EFFORT", "medium"),
        ReplicateVerbosity:           getEnv("PG_REPLICATE_VERBOSITY", "medium"),
        ReplicateMaxCompletionTokens: getInt("PG_REPLICATE_MAX_COMPLETION_TOKENS", 2048),
        ReplicateTemperature:         getFloat("PG_REPLICATE_TEMPERATURE", 0.2),
        ReplicateTopP:                getFloat("PG_REPLICATE_TOP_P", 1.0),
        ReplicatePresencePenalty:     getFloat("PG_REPLICATE_PRESENCE_PENALTY", 0.0),
        ReplicateFrequencyPenalty:    getFloat("PG_REPLICATE_FREQUENCY_PENALTY", 0.0),
        ReplicateTimeout:             getDuration("PG_REPLICATE_TIMEOUT", 120*time.Second),

        APIAddr: getEnv("PG_API_ADDR", ":3001"),

        APIKeySalt: getEnv("PG_API_KEY_SALT", ""),

        DefaultUpstreamHost:   getEnv("PG_DEFAULT_UPSTREAM_HOST", ""),
        DefaultUpstreamScheme: getEnv("PG_DEFAULT_UPSTREAM_SCHEME", "https"),
    }
}

func getEnv(key, fallback string) string {
    if val := os.Getenv(key); val != "" {
        return val
    }
    return fallback
}

func getInt(key string, fallback int) int {
    if val := os.Getenv(key); val != "" {
        if parsed, err := strconv.Atoi(val); err == nil {
            return parsed
        }
    }
    return fallback
}

func getBool(key string, fallback bool) bool {
    if val := os.Getenv(key); val != "" {
        if parsed, err := strconv.ParseBool(val); err == nil {
            return parsed
        }
    }
    return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
    if val := os.Getenv(key); val != "" {
        if parsed, err := time.ParseDuration(val); err == nil {
            return parsed
        }
    }
    return fallback
}

func getFloat(key string, fallback float64) float64 {
    if val := os.Getenv(key); val != "" {
        if parsed, err := strconv.ParseFloat(val, 64); err == nil {
            return parsed
        }
    }
    return fallback
}
