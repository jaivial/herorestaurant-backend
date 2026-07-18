package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type MySQLConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
}

type Config struct {
	Addr                        string
	StaticDir                   string
	CORSAllowOrigins            string
	AdminToken                  string
	BunnyPullBaseURL            string
	BunnyStorageZone            string
	BunnyStorageKey             string
	BunnyMemberPullBaseURL      string
	BunnyMemberStorageZone      string
	BunnyMemberStorageKey       string
	CloudflareAPIToken          string
	CloudflareAccountID         string
	CloudflareZoneID            string
	OpenAIAPIKey                string
	OpenAIImageEditModel        string
	OpenAIImageEditURL          string
	OpenAITimeout               time.Duration
	OpenAIFetchTimeout          time.Duration
	OpenAIMaxInputBytes         int
	OpenAIMaxOutputBytes        int
	OpenAIConcurrency           int
	MiniMaxAPIKey               string
	MiniMaxBaseURL              string
	MiniMaxModel                string
	MiniMaxTranslateTimeout     time.Duration
	MiniMaxTranslateConcurrency int
	BotModel                    string
	BotTimeout                  time.Duration
	BotMaxTokens                int
	BotMaxIterations            int
	BotHistoryLimit             int
	BotDailyTurnsCap            int
	BotPublicWebhookURL         string
	SMTPHost                    string
	SMTPPort                    int
	SMTPUsername                string
	SMTPPassword                string
	MySQL                       MySQLConfig
}

func Load() Config {
	port := getenv("PORT", "8080")
	defaultPull := getenv("BUNNY_PULL_BASE_URL", "https://villacarmenmedia.b-cdn.net")
	defaultMembersPull := getenv("BUNNY_MEMBERS_PULL_BASE_URL", "https://herorestaurantmedia.b-cdn.net")
	defaultKey := os.Getenv("BUNNY_STORAGE_ACCESS_KEY")

	return Config{
		Addr:                        ":" + port,
		StaticDir:                   os.Getenv("STATIC_DIR"),
		CORSAllowOrigins:            os.Getenv("CORS_ALLOW_ORIGINS"),
		AdminToken:                  os.Getenv("ADMIN_TOKEN"),
		BunnyPullBaseURL:            defaultPull,
		BunnyStorageZone:            getenv("BUNNY_STORAGE_ZONE", "villacarmen"),
		BunnyStorageKey:             defaultKey,
		BunnyMemberPullBaseURL:      defaultMembersPull,
		BunnyMemberStorageZone:      getenv("BUNNY_MEMBERS_STORAGE_ZONE", "herorestaurant"),
		BunnyMemberStorageKey:       getenv("BUNNY_MEMBERS_STORAGE_ACCESS_KEY", defaultKey),
		CloudflareAPIToken:          os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareAccountID:         os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		CloudflareZoneID:            os.Getenv("CLOUDFLARE_ZONE_ID"),
		OpenAIAPIKey:                strings.TrimSpace(getenvFirst([]string{"WAVESPEED_API_KEY"}, "")),
		OpenAIImageEditModel:        getenvFirst([]string{"WAVESPEED_IMAGE_EDIT_MODEL", "OPENAI_IMAGE_EDIT_MODEL", "OPENAI_IMAGE_MODEL"}, "openai/gpt-image-1.5/edit"),
		OpenAIImageEditURL:          getenvFirst([]string{"WAVESPEED_IMAGE_EDIT_URL", "OPENAI_IMAGE_EDIT_URL", "OPENAI_IMAGE_URL"}, "https://api.wavespeed.ai/api/v3/openai/gpt-image-1.5/edit"),
		OpenAITimeout:               time.Duration(getenvIntFirst([]string{"WAVESPEED_IMAGE_TIMEOUT_SECONDS", "OPENAI_IMAGE_TIMEOUT_SECONDS", "OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS"}, 180, 5, 600)) * time.Second,
		OpenAIFetchTimeout:          time.Duration(getenvIntFirst([]string{"WAVESPEED_IMAGE_FETCH_TIMEOUT_SECONDS", "OPENAI_IMAGE_FETCH_TIMEOUT_SECONDS", "OPENAI_IMAGE_EDIT_FETCH_TIMEOUT_SECONDS"}, 30, 5, 180)) * time.Second,
		OpenAIMaxInputBytes:         getenvIntFirst([]string{"WAVESPEED_IMAGE_MAX_INPUT_BYTES", "OPENAI_IMAGE_MAX_INPUT_BYTES"}, 8<<20, 1<<20, 32<<20),
		OpenAIMaxOutputBytes:        getenvIntFirst([]string{"WAVESPEED_IMAGE_MAX_OUTPUT_BYTES", "OPENAI_IMAGE_MAX_OUTPUT_BYTES"}, 8<<20, 64*1024, 64<<20),
		OpenAIConcurrency:           getenvIntFirst([]string{"WAVESPEED_IMAGE_CONCURRENCY", "OPENAI_IMAGE_CONCURRENCY", "OPENAI_IMAGE_EDIT_CONCURRENCY"}, 2, 1, 32),
		MiniMaxAPIKey:               strings.TrimSpace(os.Getenv("MINIMAX_API_KEY")),
		MiniMaxBaseURL:              strings.TrimRight(getenv("MINIMAX_BASE_URL", "https://api.minimax.io/anthropic"), "/"),
		MiniMaxModel:                getenv("MINIMAX_MODEL", "MiniMax-M3"),
		MiniMaxTranslateTimeout:     time.Duration(getenvInt("MINIMAX_TRANSLATE_TIMEOUT_SECONDS", 20, 5, 120)) * time.Second,
		MiniMaxTranslateConcurrency: getenvInt("MINIMAX_TRANSLATE_CONCURRENCY", 4, 1, 32),
		BotModel:                    getenv("BOT_MINIMAX_MODEL", getenv("MINIMAX_MODEL", "MiniMax-M3")),
		BotTimeout:                  time.Duration(getenvInt("BOT_MINIMAX_TIMEOUT_SECONDS", 45, 5, 300)) * time.Second,
		BotMaxTokens:                getenvInt("BOT_MINIMAX_MAX_TOKENS", 1024, 128, 8192),
		BotMaxIterations:            getenvInt("BOT_MAX_ITERATIONS", 8, 1, 20),
		BotHistoryLimit:             getenvInt("BOT_HISTORY_LIMIT", 20, 2, 100),
		BotDailyTurnsCap:            getenvInt("BOT_DAILY_TURNS_CAP", 2000, 1, 1000000),
		BotPublicWebhookURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("BOT_PUBLIC_WEBHOOK_URL")), "/"),
		SMTPHost:                    strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:                    getenvInt("SMTP_PORT", 587, 1, 65535),
		SMTPUsername:                strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword:                os.Getenv("SMTP_PASSWORD"),
		MySQL: MySQLConfig{
			Host:     getenv("DB_HOST", "127.0.0.1"),
			Port:     getenv("DB_PORT", "3306"),
			User:     getenv("DB_USER", "villacarmen"),
			Password: getenv("DB_PASSWORD", "villacarmen"),
			DBName:   getenv("DB_NAME", "villacarmen"),
		},
	}
}

func getenv(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

func getenvFirst(keys []string, fallback string) string {
	for _, key := range keys {
		val := strings.TrimSpace(os.Getenv(key))
		if val != "" {
			return val
		}
	}
	return fallback
}

func getenvInt(key string, fallback int, min int, max int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if min > 0 && v < min {
		return fallback
	}
	if max > 0 && v > max {
		return fallback
	}
	return v
}

func getenvIntFirst(keys []string, fallback int, min int, max int) int {
	for _, key := range keys {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return fallback
		}
		if min > 0 && v < min {
			return fallback
		}
		if max > 0 && v > max {
			return fallback
		}
		return v
	}
	return fallback
}
