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
	Addr                   string
	StaticDir              string
	CORSAllowOrigins       string
	AdminToken             string
	BunnyPullBaseURL       string
	BunnyStorageZone       string
	BunnyStorageKey        string
	BunnyMemberPullBaseURL string
	BunnyMemberStorageZone string
	BunnyMemberStorageKey  string
	UazapiUrl              string
	UazapiToken            string
	MySQL                  MySQLConfig
}

func Load() Config {
	port := getenv("PORT", "8080")
	defaultPull := getenv("BUNNY_PULL_BASE_URL", "https://villacarmenmedia.b-cdn.net")
	defaultMembersPull := getenv("BUNNY_MEMBERS_PULL_BASE_URL", "https://herorestaurantmedia.b-cdn.net")
	defaultKey := os.Getenv("BUNNY_STORAGE_ACCESS_KEY")

	return Config{
		Addr:                   ":" + port,
		StaticDir:              os.Getenv("STATIC_DIR"),
		CORSAllowOrigins:       os.Getenv("CORS_ALLOW_ORIGINS"),
		AdminToken:             os.Getenv("ADMIN_TOKEN"),
		BunnyPullBaseURL:       defaultPull,
		BunnyStorageZone:       getenv("BUNNY_STORAGE_ZONE", "villacarmen"),
		BunnyStorageKey:        defaultKey,
		BunnyMemberPullBaseURL: defaultMembersPull,
		BunnyMemberStorageZone: getenv("BUNNY_MEMBERS_STORAGE_ZONE", "herorestaurant"),
		BunnyMemberStorageKey:  getenv("BUNNY_MEMBERS_STORAGE_ACCESS_KEY", defaultKey),
		UazapiUrl:              getenv("UAZAPI_URL", ""),
		UazapiToken:            getenv("UAZAPI_TOKEN", ""),
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
