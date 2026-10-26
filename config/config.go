package config

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	defaultDBPath   = "whatsmeow.db"
	defaultLogLevel = "INFO"
)

var (
	adminNumberPattern = regexp.MustCompile(`^[0-9]+$`)
	groupJIDPattern    = regexp.MustCompile(`^[0-9]+@g\.us$`)
	validLogLevels     = map[string]bool{
		"TRACE":    true,
		"DEBUG":    true,
		"INFO":     true,
		"WARN":     true,
		"ERROR":    true,
		"DISABLED": true,
	}
)

type Config struct {
	AdminNumbers  []string
	AllowedGroups []string
	DBPath        string
	LogLevel      string
}

func Load(getenv func(string) string) (Config, error) {
	adminNumbers, err := parseRequiredList(
		"ASG_BOT_ADMIN_NUMBERS",
		getenv("ASG_BOT_ADMIN_NUMBERS"),
		adminNumberPattern,
	)
	if err != nil {
		return Config{}, err
	}

	allowedGroups, err := parseRequiredList(
		"ASG_BOT_ALLOWED_GROUPS",
		getenv("ASG_BOT_ALLOWED_GROUPS"),
		groupJIDPattern,
	)
	if err != nil {
		return Config{}, err
	}

	dbPath := strings.TrimSpace(getenv("ASG_BOT_DB_PATH"))
	if dbPath == "" {
		dbPath = defaultDBPath
	}

	logLevel := strings.TrimSpace(getenv("ASG_BOT_LOG_LEVEL"))
	if logLevel == "" {
		logLevel = defaultLogLevel
	}
	if !validLogLevels[logLevel] {
		return Config{}, fmt.Errorf("ASG_BOT_LOG_LEVEL has unsupported value %q", logLevel)
	}

	return Config{
		AdminNumbers:  adminNumbers,
		AllowedGroups: allowedGroups,
		DBPath:        dbPath,
		LogLevel:      logLevel,
	}, nil
}

func parseRequiredList(name, raw string, pattern *regexp.Regexp) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s is required", name)
	}

	values := strings.Split(raw, ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
		if values[i] == "" {
			return nil, fmt.Errorf("%s contains an empty entry", name)
		}
		if !pattern.MatchString(values[i]) {
			return nil, fmt.Errorf("%s contains invalid value %q", name, values[i])
		}
	}
	return values, nil
}
