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
