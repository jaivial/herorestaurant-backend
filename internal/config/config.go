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
	BunnyPrivateStorageZone     string
	BunnyPrivateStorageKey      string
	StockDocumentRetentionDays  int
	CloudflareAPIToken          string
	CloudflareAPIEmail          string
	CloudflareAPIKey            string
	CloudflareAccountID         string
	CloudflareZoneID            string
	StripeSecretKey             string
	StripeWebhookSecret         string
	RegisterAPIToken            string // security token required on domain-register (anti-abuse)
	InstaticBasePort            int
	InstaticBaseDir             string
	InstaticServerDir           string
	InstaticMaxInstances        int
	InstaticSeedAdminEmail      string
	InstaticSeedAdminPassword   string
	OpenAIAPIKey                string
	OpenAIImageEditModel        string
	OpenAIImageEditURL          string
	OpenAITimeout               time.Duration
	OpenAIFetchTimeout          time.Duration
	OpenAIMaxInputBytes         int
	OpenAIMaxOutputBytes        int
	OpenAIConcurrency           int
	PreShiftReminderMinutes     int
	StockDigestHour             int
	MiniMaxAPIKey               string
	MiniMaxBaseURL              string
	MiniMaxModel                string
	VaultToken                  string
	MiniMaxTranslateTimeout     time.Duration
	MiniMaxTranslateConcurrency int
	StockOCRProvider            string
	PaddleOCRGatewayURL         string
	PaddleOCRModel              string
	PaddleOCRTimeout            time.Duration
	BotModel                    string
	BotTimeout                  time.Duration
	BotMaxTokens                int
	BotMaxIterations            int
	BotContextSQLitePath        string
	BotDailyTurnsCap            int
	AssistantModel              string
	AssistantTimeout            time.Duration
	AssistantMaxTokens          int
	AssistantHistoryLimit       int
	AssistantPublicRateLimit    int
	BotPublicWebhookURL         string
	EvolutionWebhookSecret      string
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
		BunnyPrivateStorageZone:     strings.TrimSpace(os.Getenv("BUNNY_PRIVATE_STORAGE_ZONE")),
		BunnyPrivateStorageKey:      strings.TrimSpace(os.Getenv("BUNNY_PRIVATE_STORAGE_ACCESS_KEY")),
		StockDocumentRetentionDays:  getenvInt("STOCK_DOCUMENT_RETENTION_DAYS", 365, 1, 3650),
		CloudflareAPIToken:          os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareAPIEmail:          os.Getenv("CLOUDFLARE_API_EMAIL"),
		CloudflareAPIKey:            os.Getenv("CLOUDFLARE_API_KEY"),
		CloudflareAccountID:         os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		CloudflareZoneID:            os.Getenv("CLOUDFLARE_ZONE_ID"),
		StripeSecretKey:             strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		StripeWebhookSecret:         strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
		RegisterAPIToken:            strings.TrimSpace(os.Getenv("REGISTER_API_TOKEN")),
		InstaticBasePort:            getenvInt("INSTATIC_BASE_PORT", 39000, 1, 65535),
		InstaticBaseDir:             getenv("INSTATIC_BASE_DIR", "/var/lib/instatic"),
		InstaticServerDir:           getenv("INSTATIC_SERVER_DIR", "/var/www/newvillacarmen/backend/third_party/instatic"),
		InstaticMaxInstances:        getenvInt("INSTATIC_MAX_INSTANCES", 8, 1, 256),
		InstaticSeedAdminEmail:      getenv("INSTATIC_SEED_ADMIN_EMAIL", "website@menustudioai.com"),
		InstaticSeedAdminPassword:   getenv("INSTATIC_SEED_ADMIN_PASSWORD", "ChangeMeWebsite1!"),
		OpenAIAPIKey:                strings.TrimSpace(getenvFirst([]string{"WAVESPEED_API_KEY"}, "")),
		OpenAIImageEditModel:        getenvFirst([]string{"WAVESPEED_IMAGE_EDIT_MODEL", "OPENAI_IMAGE_EDIT_MODEL", "OPENAI_IMAGE_MODEL"}, "openai/gpt-image-1.5/edit"),
		OpenAIImageEditURL:          getenvFirst([]string{"WAVESPEED_IMAGE_EDIT_URL", "OPENAI_IMAGE_EDIT_URL", "OPENAI_IMAGE_URL"}, "https://api.wavespeed.ai/api/v3/openai/gpt-image-1.5/edit"),
		OpenAITimeout:               time.Duration(getenvIntFirst([]string{"WAVESPEED_IMAGE_TIMEOUT_SECONDS", "OPENAI_IMAGE_TIMEOUT_SECONDS", "OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS"}, 180, 5, 600)) * time.Second,
		OpenAIFetchTimeout:          time.Duration(getenvIntFirst([]string{"WAVESPEED_IMAGE_FETCH_TIMEOUT_SECONDS", "OPENAI_IMAGE_FETCH_TIMEOUT_SECONDS", "OPENAI_IMAGE_EDIT_FETCH_TIMEOUT_SECONDS"}, 30, 5, 180)) * time.Second,
		OpenAIMaxInputBytes:         getenvIntFirst([]string{"WAVESPEED_IMAGE_MAX_INPUT_BYTES", "OPENAI_IMAGE_MAX_INPUT_BYTES"}, 8<<20, 1<<20, 32<<20),
		OpenAIMaxOutputBytes:        getenvIntFirst([]string{"WAVESPEED_IMAGE_MAX_OUTPUT_BYTES", "OPENAI_IMAGE_MAX_OUTPUT_BYTES"}, 8<<20, 64*1024, 64<<20),
		OpenAIConcurrency:           getenvIntFirst([]string{"WAVESPEED_IMAGE_CONCURRENCY", "OPENAI_IMAGE_CONCURRENCY", "OPENAI_IMAGE_EDIT_CONCURRENCY"}, 2, 1, 32),
		PreShiftReminderMinutes:     getenvIntFirst([]string{"PRE_SHIFT_REMINDER_MINUTES", "PRESHIFT_REMINDER_MINUTES"}, 10, 5, 10),
		StockDigestHour:             getenvIntFirst([]string{"STOCK_DIGEST_HOUR"}, 8, 0, 23),
		MiniMaxAPIKey:               strings.TrimSpace(os.Getenv("MINIMAX_API_KEY")),
		MiniMaxBaseURL:              strings.TrimRight(getenv("MINIMAX_BASE_URL", "https://api.minimax.io/anthropic"), "/"),
		MiniMaxModel:                getenv("MINIMAX_MODEL", "MiniMax-M3"),
		VaultToken:                  strings.TrimSpace(os.Getenv("VAULT_TOKEN")),
		MiniMaxTranslateTimeout:     time.Duration(getenvInt("MINIMAX_TRANSLATE_TIMEOUT_SECONDS", 20, 5, 120)) * time.Second,
		MiniMaxTranslateConcurrency: getenvInt("MINIMAX_TRANSLATE_CONCURRENCY", 4, 1, 32),
		StockOCRProvider:            strings.ToLower(strings.TrimSpace(getenv("STOCK_OCR_PROVIDER", "minimax"))),
		PaddleOCRGatewayURL:         strings.TrimRight(strings.TrimSpace(getenv("PADDLEOCR_GATEWAY_URL", "http://127.0.0.1:8090")), "/"),
		PaddleOCRModel:              getenv("PADDLEOCR_MODEL", "PaddleOCR-VL-1.6"),
		PaddleOCRTimeout:            time.Duration(getenvInt("PADDLEOCR_TIMEOUT_SECONDS", 180, 5, 900)) * time.Second,
		BotModel:                    getenv("BOT_MINIMAX_MODEL", getenv("MINIMAX_MODEL", "MiniMax-M3")),
		BotTimeout:                  time.Duration(getenvInt("BOT_MINIMAX_TIMEOUT_SECONDS", 45, 5, 300)) * time.Second,
		BotMaxTokens:                getenvInt("BOT_MINIMAX_MAX_TOKENS", 1024, 128, 8192),
		BotMaxIterations:            getenvInt("BOT_MAX_ITERATIONS", 8, 1, 20),
		BotContextSQLitePath:        strings.TrimSpace(getenv("BOT_CONTEXT_SQLITE_PATH", "./data/whatsapp-bot-context.sqlite")),
		BotDailyTurnsCap:            getenvInt("BOT_DAILY_TURNS_CAP", 2000, 1, 1000000),
		AssistantModel:              getenv("ASSISTANT_MINIMAX_MODEL", getenv("MINIMAX_MODEL", "MiniMax-M3")),
		AssistantTimeout:            time.Duration(getenvInt("ASSISTANT_TIMEOUT_SECONDS", 60, 5, 600)) * time.Second,
		AssistantMaxTokens:          getenvInt("ASSISTANT_MAX_TOKENS", 1024, 128, 8192),
		AssistantHistoryLimit:       getenvInt("ASSISTANT_HISTORY_LIMIT", 20, 2, 100),
		AssistantPublicRateLimit:    getenvInt("ASSISTANT_PUBLIC_RATE_LIMIT", 20, 1, 1000),
		BotPublicWebhookURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("BOT_PUBLIC_WEBHOOK_URL")), "/"),
		EvolutionWebhookSecret:      strings.TrimSpace(os.Getenv("EVOLUTION_WEBHOOK_SECRET")),
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
