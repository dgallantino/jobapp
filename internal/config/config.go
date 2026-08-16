package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds runtime settings loaded from the environment.
type Config struct {
	DBPath                 string
	ListenAddr             string
	SitePasswordHash       string // bcrypt hash
	SessionSecret          string
	OpenRouterAPIKey       string
	OpenRouterModel        string
	OpenRouterSystemPrompt string // cover-letter system prompt; empty uses llm fallback
	TelegramBotToken       string
	TelegramChatID         int64
	ScrapeConcurrency      int    // max concurrent detail fetches per listing scrape
	ChromePath             string // Chromium/Chrome binary for chromedp (Glints listing)
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	cfg := Config{
		DBPath:                 envOr("JOBAPP_DB", "jobs.db"),
		ListenAddr:             envOr("JOBAPP_LISTEN", ":8080"),
		SitePasswordHash:       os.Getenv("JOBAPP_PASSWORD_HASH"),
		SessionSecret:          os.Getenv("JOBAPP_SESSION_SECRET"),
		OpenRouterAPIKey:       os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterModel:        envOr("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
		OpenRouterSystemPrompt: strings.TrimSpace(os.Getenv("OPENROUTER_SYSTEM_PROMPT")),
		TelegramBotToken:       os.Getenv("TELEGRAM_BOT_TOKEN"),
		ScrapeConcurrency:      5,
		ChromePath:             strings.TrimSpace(os.Getenv("JOBAPP_CHROME_PATH")),
	}

	if chatID := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")); chatID != "" {
		n, err := strconv.ParseInt(chatID, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("TELEGRAM_CHAT_ID: %w", err)
		}
		cfg.TelegramChatID = n
	}

	if raw := strings.TrimSpace(os.Getenv("JOBAPP_SCRAPE_CONCURRENCY")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("JOBAPP_SCRAPE_CONCURRENCY: %w", err)
		}
		if n < 1 {
			return Config{}, fmt.Errorf("JOBAPP_SCRAPE_CONCURRENCY: must be >= 1")
		}
		cfg.ScrapeConcurrency = n
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
