package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

type Config struct {
	Addr          string
	DBPath        string
	SecretKey     []byte
	AllowedLogins map[string]bool
	GitHubURL     string
	OpenRouterURL string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:          getenv("SHEEPDOG_ADDR"),
		DBPath:        getenv("SHEEPDOG_DB"),
		AllowedLogins: map[string]bool{},
		GitHubURL:     "https://api.github.com",
		OpenRouterURL: "https://openrouter.ai/api/v1",
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8080"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "sheepdog.db"
	}

	rawKey := strings.TrimSpace(getenv("SHEEPDOG_SECRET_KEY"))
	if rawKey == "" {
		return Config{}, errors.New("SHEEPDOG_SECRET_KEY is required: set it to the output of `openssl rand -base64 32`")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("SHEEPDOG_SECRET_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return Config{}, fmt.Errorf("SHEEPDOG_SECRET_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.SecretKey = key

	for login := range strings.SplitSeq(getenv("SHEEPDOG_ALLOWED_LOGINS"), ",") {
		if login = strings.TrimSpace(login); login != "" {
			cfg.AllowedLogins[strings.ToLower(login)] = true
		}
	}
	if len(cfg.AllowedLogins) == 0 {
		return Config{}, errors.New("SHEEPDOG_ALLOWED_LOGINS is required: set it to a comma-separated list of GitHub logins")
	}
	return cfg, nil
}

func (c Config) Allowed(login string) bool {
	return c.AllowedLogins[strings.ToLower(login)]
}
