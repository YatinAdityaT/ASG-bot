package config

import (
	"reflect"
	"testing"
)

func TestLoadValid(t *testing.T) {
	env := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":  "111, 222",
		"ASG_BOT_ALLOWED_GROUPS": "123@g.us, 456@g.us",
	}

	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"111", "222"}; !reflect.DeepEqual(cfg.AdminNumbers, want) {
		t.Fatalf("AdminNumbers = %#v, want %#v", cfg.AdminNumbers, want)
	}
	if want := []string{"123@g.us", "456@g.us"}; !reflect.DeepEqual(cfg.AllowedGroups, want) {
		t.Fatalf("AllowedGroups = %#v, want %#v", cfg.AllowedGroups, want)
	}
	if cfg.DBPath != "whatsmeow.db" || cfg.LogLevel != "INFO" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadRequiresValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "admin numbers",
			env: map[string]string{
				"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
			},
		},
		{
			name: "allowed groups",
			env: map[string]string{
				"ASG_BOT_ADMIN_NUMBERS": "111",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(func(key string) string { return tt.env[key] }); err == nil {
				t.Fatal("Load() error = nil, want required-value error")
			}
		})
	}
}

func TestLoadRejectsEmptyListEntries(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "admin numbers", key: "ASG_BOT_ADMIN_NUMBERS"},
		{name: "allowed groups", key: "ASG_BOT_ALLOWED_GROUPS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{
				"ASG_BOT_ADMIN_NUMBERS":  "111",
				"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
			}
			env[tt.key] += ", "

			if _, err := Load(func(key string) string { return env[key] }); err == nil {
				t.Fatal("Load() error = nil, want empty-entry error")
			}
		})
	}
}

func TestLoadValidatesAdminNumbers(t *testing.T) {
	env := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":  "+111",
		"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
	}

	if _, err := Load(func(key string) string { return env[key] }); err == nil {
		t.Fatal("Load() error = nil, want invalid phone-number error")
	}
}

func TestLoadValidatesGroupJIDs(t *testing.T) {
	env := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":  "111",
		"ASG_BOT_ALLOWED_GROUPS": "family@g.us",
	}

	if _, err := Load(func(key string) string { return env[key] }); err == nil {
		t.Fatal("Load() error = nil, want invalid group-JID error")
	}
}

func TestLoadAcceptsSupportedLogLevels(t *testing.T) {
	for _, level := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "DISABLED"} {
		t.Run(level, func(t *testing.T) {
			env := map[string]string{
				"ASG_BOT_ADMIN_NUMBERS":  "111",
				"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
				"ASG_BOT_LOG_LEVEL":      " " + level + " ",
				"ASG_BOT_DB_PATH":        " custom.db ",
			}

			cfg, err := Load(func(key string) string { return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LogLevel != level || cfg.DBPath != "custom.db" {
				t.Fatalf("optional values not trimmed: %#v", cfg)
			}
		})
	}
}

func TestLoadRejectsUnsupportedLogLevel(t *testing.T) {
	env := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":  "111",
		"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
		"ASG_BOT_LOG_LEVEL":      "verbose",
	}

	if _, err := Load(func(key string) string { return env[key] }); err == nil {
		t.Fatal("Load() error = nil, want unsupported log-level error")
	}
}

func TestLoadAPIDisabledByDefault(t *testing.T) {
	env := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":  "111",
		"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
	}

	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIEnabled {
		t.Fatal("APIEnabled = true, want false")
	}
	if cfg.APIAddr != "" || cfg.APIToken != "" || cfg.APITargetGroup != "" || cfg.APIAllowedOrigins != nil {
		t.Fatalf("disabled API fields unexpectedly populated: %#v", cfg)
	}
}

func TestLoadValidEnabledAPI(t *testing.T) {
	env := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":       "111",
		"ASG_BOT_ALLOWED_GROUPS":      "123@g.us,456@g.us",
		"ASG_BOT_API_ENABLED":         "true",
		"ASG_BOT_API_TOKEN":           "01234567890123456789012345678901",
		"ASG_BOT_API_TARGET_GROUP":    "456@g.us",
		"ASG_BOT_API_ALLOWED_ORIGINS": " chrome-extension://abcdefghijklmnop ,chrome-extension://qrstuvwxyzabcdef ",
	}

	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.APIEnabled {
		t.Fatal("APIEnabled = false, want true")
	}
	if cfg.APIAddr != "127.0.0.1:8080" {
		t.Fatalf("APIAddr = %q, want default", cfg.APIAddr)
	}
	if cfg.APIToken != env["ASG_BOT_API_TOKEN"] || cfg.APITargetGroup != "456@g.us" {
		t.Fatalf("unexpected API configuration: %#v", cfg)
	}
	wantOrigins := []string{"chrome-extension://abcdefghijklmnop", "chrome-extension://qrstuvwxyzabcdef"}
	if !reflect.DeepEqual(cfg.APIAllowedOrigins, wantOrigins) {
		t.Fatalf("APIAllowedOrigins = %#v, want %#v", cfg.APIAllowedOrigins, wantOrigins)
	}
}

func TestLoadRejectsInvalidEnabledAPI(t *testing.T) {
	valid := map[string]string{
		"ASG_BOT_ADMIN_NUMBERS":    "111",
		"ASG_BOT_ALLOWED_GROUPS":   "123@g.us",
		"ASG_BOT_API_ENABLED":      "true",
		"ASG_BOT_API_ADDR":         "127.0.0.1:9090",
		"ASG_BOT_API_TOKEN":        "01234567890123456789012345678901",
		"ASG_BOT_API_TARGET_GROUP": "123@g.us",
	}
	tests := []struct {
		name   string
		key    string
		value  string
		delete bool
	}{
		{name: "missing token", key: "ASG_BOT_API_TOKEN", delete: true},
		{name: "short token", key: "ASG_BOT_API_TOKEN", value: "too-short"},
		{name: "malformed address", key: "ASG_BOT_API_ADDR", value: "127.0.0.1"},
		{name: "malformed target", key: "ASG_BOT_API_TARGET_GROUP", value: "123@s.whatsapp.net"},
		{name: "target not allowed", key: "ASG_BOT_API_TARGET_GROUP", value: "456@g.us"},
		{name: "malformed origin", key: "ASG_BOT_API_ALLOWED_ORIGINS", value: "https://example.com"},
		{name: "origin with path", key: "ASG_BOT_API_ALLOWED_ORIGINS", value: "chrome-extension://abcdefghijklmnop/path"},
		{name: "empty origin entry", key: "ASG_BOT_API_ALLOWED_ORIGINS", value: "chrome-extension://abcdefghijklmnop,"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(valid))
			for key, value := range valid {
				env[key] = value
			}
			if tt.delete {
				delete(env, tt.key)
			} else {
				env[tt.key] = tt.value
			}

			if _, err := Load(func(key string) string { return env[key] }); err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}

func TestLoadRejectsMalformedAPIEnabled(t *testing.T) {
	for _, value := range []string{"yes", "1", "t", "T", "0", "f", "F", "TRUE-ish"} {
		t.Run(value, func(t *testing.T) {
			env := map[string]string{
				"ASG_BOT_ADMIN_NUMBERS":    "111",
				"ASG_BOT_ALLOWED_GROUPS":   "123@g.us",
				"ASG_BOT_API_ENABLED":      value,
				"ASG_BOT_API_TOKEN":        "01234567890123456789012345678901",
				"ASG_BOT_API_TARGET_GROUP": "123@g.us",
			}

			if _, err := Load(func(key string) string { return env[key] }); err == nil {
				t.Fatal("Load() error = nil, want boolean validation error")
			}
		})
	}
}

func TestLoadAPIEnabledAcceptsCaseInsensitiveLiterals(t *testing.T) {
	for _, value := range []string{"false", "FALSE", " False "} {
		t.Run(value, func(t *testing.T) {
			env := map[string]string{
				"ASG_BOT_ADMIN_NUMBERS":  "111",
				"ASG_BOT_ALLOWED_GROUPS": "123@g.us",
				"ASG_BOT_API_ENABLED":    value,
			}

			cfg, err := Load(func(key string) string { return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			if cfg.APIEnabled {
				t.Fatal("APIEnabled = true, want false")
			}
		})
	}
}
