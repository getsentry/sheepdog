package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

var validKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SHEEPDOG_SECRET_KEY":     validKey,
		"SHEEPDOG_ALLOWED_LOGINS": " Alwx , octocat,,",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" || cfg.DBPath != "sheepdog.db" {
		t.Errorf("defaults: addr %q, db %q", cfg.Addr, cfg.DBPath)
	}
	if cfg.GitHubURL != "https://api.github.com" || cfg.OpenRouterURL != "https://openrouter.ai/api/v1" {
		t.Errorf("base URLs: %q, %q", cfg.GitHubURL, cfg.OpenRouterURL)
	}
	if len(cfg.SecretKey) != 32 {
		t.Errorf("key length %d", len(cfg.SecretKey))
	}
	if !cfg.Allowed("alwx") || !cfg.Allowed("OCTOCAT") || cfg.Allowed("someone") || len(cfg.AllowedLogins) != 2 {
		t.Errorf("allowed logins: %v", cfg.AllowedLogins)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SHEEPDOG_ADDR":           "0.0.0.0:9000",
		"SHEEPDOG_DB":             "/tmp/x.db",
		"SHEEPDOG_SECRET_KEY":     validKey,
		"SHEEPDOG_ALLOWED_LOGINS": "alwx",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:9000" || cfg.DBPath != "/tmp/x.db" {
		t.Errorf("addr %q, db %q", cfg.Addr, cfg.DBPath)
	}
}

func TestLoadErrorsNameTheVariable(t *testing.T) {
	cases := map[string]struct {
		values  map[string]string
		varName string
	}{
		"missing key":    {map[string]string{"SHEEPDOG_ALLOWED_LOGINS": "alwx"}, "SHEEPDOG_SECRET_KEY"},
		"short key":      {map[string]string{"SHEEPDOG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16)), "SHEEPDOG_ALLOWED_LOGINS": "alwx"}, "SHEEPDOG_SECRET_KEY"},
		"invalid base64": {map[string]string{"SHEEPDOG_SECRET_KEY": "not base64!", "SHEEPDOG_ALLOWED_LOGINS": "alwx"}, "SHEEPDOG_SECRET_KEY"},
		"missing logins": {map[string]string{"SHEEPDOG_SECRET_KEY": validKey}, "SHEEPDOG_ALLOWED_LOGINS"},
		"empty logins":   {map[string]string{"SHEEPDOG_SECRET_KEY": validKey, "SHEEPDOG_ALLOWED_LOGINS": " , "}, "SHEEPDOG_ALLOWED_LOGINS"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(c.values))
			if err == nil || !strings.Contains(err.Error(), c.varName) {
				t.Fatalf("got %v, want an error that names %s", err, c.varName)
			}
		})
	}
}
