package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds runtime settings loaded from the environment.
type Config struct {
	DBPath           string
	ListenAddr       string
	SitePasswordHash string // bcrypt hash
	SessionSecret    string
	OpenRouterAPIKey string
	OpenRouterModel  string
	TelegramBotToken string
	TelegramChatID   int64
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	cfg := Config{
		DBPath:           envOr("JOBAPP_DB", "jobs.db"),
		ListenAddr:       envOr("JOBAPP_LISTEN", ":8080"),
		SitePasswordHash: os.Getenv("JOBAPP_PASSWORD_HASH"),
		SessionSecret:    os.Getenv("JOBAPP_SESSION_SECRET"),
		OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterModel:  envOr("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
	}

	if chatID := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")); chatID != "" {
		n, err := strconv.ParseInt(chatID, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("TELEGRAM_CHAT_ID: %w", err)
		}
		cfg.TelegramChatID = n
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
