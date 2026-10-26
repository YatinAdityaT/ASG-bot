package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	defaultDBPath   = "whatsmeow.db"
	defaultLogLevel = "INFO"
	defaultAPIAddr  = "127.0.0.1:8080"
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
	AdminNumbers      []string
	AllowedGroups     []string
	DBPath            string
	LogLevel          string
	APIEnabled        bool
	APIAddr           string
	APIToken          string
	APITargetGroup    string
	APIAllowedOrigins []string
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

	cfg := Config{
		AdminNumbers:  adminNumbers,
		AllowedGroups: allowedGroups,
		DBPath:        dbPath,
		LogLevel:      logLevel,
	}

	apiEnabledRaw := strings.TrimSpace(getenv("ASG_BOT_API_ENABLED"))
	if apiEnabledRaw != "" {
		cfg.APIEnabled, err = strconv.ParseBool(apiEnabledRaw)
		if err != nil {
			return Config{}, fmt.Errorf("ASG_BOT_API_ENABLED must be true or false: %w", err)
		}
	}
	if !cfg.APIEnabled {
		return cfg, nil
	}

	cfg.APIAddr = strings.TrimSpace(getenv("ASG_BOT_API_ADDR"))
	if cfg.APIAddr == "" {
		cfg.APIAddr = defaultAPIAddr
	}
	if err := validateAPIAddr(cfg.APIAddr); err != nil {
		return Config{}, err
	}

	cfg.APIToken = strings.TrimSpace(getenv("ASG_BOT_API_TOKEN"))
	if utf8.RuneCountInString(cfg.APIToken) < 32 {
		return Config{}, fmt.Errorf("ASG_BOT_API_TOKEN must contain at least 32 characters")
	}

	cfg.APITargetGroup = strings.TrimSpace(getenv("ASG_BOT_API_TARGET_GROUP"))
	if !groupJIDPattern.MatchString(cfg.APITargetGroup) {
		return Config{}, fmt.Errorf("ASG_BOT_API_TARGET_GROUP must be a group JID")
	}
	if !contains(allowedGroups, cfg.APITargetGroup) {
		return Config{}, fmt.Errorf("ASG_BOT_API_TARGET_GROUP must appear in ASG_BOT_ALLOWED_GROUPS")
	}

	cfg.APIAllowedOrigins, err = parseAPIOrigins(getenv("ASG_BOT_API_ALLOWED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateAPIAddr(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("ASG_BOT_API_ADDR must be a host:port address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("ASG_BOT_API_ADDR contains an invalid port")
	}
	return nil
}

func parseAPIOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	values := strings.Split(raw, ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
		origin, err := url.Parse(values[i])
		if err != nil || origin.Scheme != "chrome-extension" || origin.Host == "" ||
			origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return nil, fmt.Errorf("ASG_BOT_API_ALLOWED_ORIGINS contains invalid origin %q", values[i])
		}
	}
	return values, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
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
