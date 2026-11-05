package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	defaultDBPath   = "whatsmeow.db"
	defaultLogLevel = "INFO"
	defaultAPIAddr  = "127.0.0.1:8080"
	defaultFilePath = "config.local.json"
)

var (
	adminNumberPattern = regexp.MustCompile(`^[0-9]+$`)
	groupJIDPattern    = regexp.MustCompile(`^[0-9]+@g\.us$`)
	meetCodePattern    = regexp.MustCompile(`^[a-z]{3}-[a-z]{4}-[a-z]{3}$`)
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
	AdminNumbers          []string
	AllowedGroups         []string
	AccountabilityGroup   string
	DBPath                string
	LogLevel              string
	APIEnabled            bool
	APIAddr               string
	MeetCode              string
	APIToken              string
	APITargetGroup        string
	MeetNotificationGroup string
	APIAllowedOrigins     []string
}

type fileConfig struct {
	AdminNumbers          []string `json:"adminNumbers"`
	AllowedGroups         []string `json:"allowedGroups"`
	AccountabilityGroup   string   `json:"accountabilityGroup"`
	DBPath                string   `json:"dbPath"`
	LogLevel              string   `json:"logLevel"`
	APIEnabled            *bool    `json:"apiEnabled"`
	APIAddr               string   `json:"apiAddr"`
	MeetCode              string   `json:"meetCode"`
	APIToken              string   `json:"apiToken"`
	APITargetGroup        string   `json:"apiTargetGroup"`
	MeetNotificationGroup string   `json:"meetNotificationGroup"`
	APIAllowedOrigins     []string `json:"apiAllowedOrigins"`
}

func Load(getenv func(string) string) (Config, error) {
	fileValues, err := loadFileConfig(getenv("ASG_BOT_CONFIG_FILE"))
	if err != nil {
		return Config{}, err
	}
	value := func(name, fileValue string) string {
		if environmentValue := strings.TrimSpace(getenv(name)); environmentValue != "" {
			return environmentValue
		}
		return fileValue
	}

	adminNumbers, err := parseRequiredList(
		"ASG_BOT_ADMIN_NUMBERS",
		value("ASG_BOT_ADMIN_NUMBERS", strings.Join(fileValues.AdminNumbers, ",")),
		adminNumberPattern,
	)
	if err != nil {
		return Config{}, err
	}

	allowedGroups, err := parseRequiredList(
		"ASG_BOT_ALLOWED_GROUPS",
		value("ASG_BOT_ALLOWED_GROUPS", strings.Join(fileValues.AllowedGroups, ",")),
		groupJIDPattern,
	)
	if err != nil {
		return Config{}, err
	}

	accountabilityGroup := strings.TrimSpace(value(
		"ASG_BOT_ACCOUNTABILITY_GROUP",
		fileValues.AccountabilityGroup,
	))
	if accountabilityGroup != "" {
		if !groupJIDPattern.MatchString(accountabilityGroup) {
			return Config{}, fmt.Errorf("ASG_BOT_ACCOUNTABILITY_GROUP must be a group JID")
		}
		if !contains(allowedGroups, accountabilityGroup) {
			allowedGroups = append(allowedGroups, accountabilityGroup)
		}
	}

	dbPath := strings.TrimSpace(value("ASG_BOT_DB_PATH", fileValues.DBPath))
	if dbPath == "" {
		dbPath = defaultDBPath
	}

	logLevel := strings.TrimSpace(value("ASG_BOT_LOG_LEVEL", fileValues.LogLevel))
	if logLevel == "" {
		logLevel = defaultLogLevel
	}
	if !validLogLevels[logLevel] {
		return Config{}, fmt.Errorf("ASG_BOT_LOG_LEVEL has unsupported value %q", logLevel)
	}

	cfg := Config{
		AdminNumbers:        adminNumbers,
		AllowedGroups:       allowedGroups,
		AccountabilityGroup: accountabilityGroup,
		DBPath:              dbPath,
		LogLevel:            logLevel,
	}

	apiEnabledRaw := strings.TrimSpace(value("ASG_BOT_API_ENABLED", boolText(fileValues.APIEnabled)))
	switch {
	case apiEnabledRaw == "", strings.EqualFold(apiEnabledRaw, "false"):
		cfg.APIEnabled = false
	case strings.EqualFold(apiEnabledRaw, "true"):
		cfg.APIEnabled = true
	default:
		return Config{}, fmt.Errorf("ASG_BOT_API_ENABLED must be true or false")
	}
	if !cfg.APIEnabled {
		return cfg, nil
	}

	cfg.APIAddr = strings.TrimSpace(value("ASG_BOT_API_ADDR", fileValues.APIAddr))
	if cfg.APIAddr == "" {
		cfg.APIAddr = defaultAPIAddr
	}
	if err := validateAPIAddr(cfg.APIAddr); err != nil {
		return Config{}, err
	}

	cfg.MeetCode = strings.TrimSpace(value("ASG_BOT_MEET_CODE", fileValues.MeetCode))
	if !meetCodePattern.MatchString(cfg.MeetCode) {
		return Config{}, fmt.Errorf("ASG_BOT_MEET_CODE must be a Google Meet code")
	}

	cfg.APIToken = strings.TrimSpace(value("ASG_BOT_API_TOKEN", fileValues.APIToken))
	if utf8.RuneCountInString(cfg.APIToken) < 32 {
		return Config{}, fmt.Errorf("ASG_BOT_API_TOKEN must contain at least 32 characters")
	}

	cfg.APITargetGroup = strings.TrimSpace(value("ASG_BOT_API_TARGET_GROUP", fileValues.APITargetGroup))
	if accountabilityGroup != "" {
		cfg.APITargetGroup = accountabilityGroup
	}
	if !groupJIDPattern.MatchString(cfg.APITargetGroup) {
		return Config{}, fmt.Errorf("ASG_BOT_API_TARGET_GROUP must be a group JID")
	}
	if !contains(allowedGroups, cfg.APITargetGroup) {
		return Config{}, fmt.Errorf("ASG_BOT_API_TARGET_GROUP must appear in ASG_BOT_ALLOWED_GROUPS")
	}

	cfg.MeetNotificationGroup = strings.TrimSpace(value(
		"ASG_BOT_MEET_NOTIFICATION_GROUP",
		fileValues.MeetNotificationGroup,
	))
	if cfg.MeetNotificationGroup == "" {
		cfg.MeetNotificationGroup = cfg.APITargetGroup
		if accountabilityGroup != "" {
			for _, group := range allowedGroups {
				if group != accountabilityGroup {
					cfg.MeetNotificationGroup = group
					break
				}
			}
		}
	}
	if !groupJIDPattern.MatchString(cfg.MeetNotificationGroup) {
		return Config{}, fmt.Errorf("ASG_BOT_MEET_NOTIFICATION_GROUP must be a group JID")
	}
	if !contains(allowedGroups, cfg.MeetNotificationGroup) {
		return Config{}, fmt.Errorf("ASG_BOT_MEET_NOTIFICATION_GROUP must appear in ASG_BOT_ALLOWED_GROUPS")
	}

	cfg.APIAllowedOrigins, err = parseAPIOrigins(value(
		"ASG_BOT_API_ALLOWED_ORIGINS",
		strings.Join(fileValues.APIAllowedOrigins, ","),
	))
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadFileConfig(configuredPath string) (fileConfig, error) {
	path := strings.TrimSpace(configuredPath)
	explicit := path != ""
	if !explicit {
		path = defaultFilePath
	}
	file, err := os.Open(path)
	if err != nil {
		if !explicit && os.IsNotExist(err) {
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var result fileConfig
	if err := decoder.Decode(&result); err != nil {
		return fileConfig{}, fmt.Errorf("decode config file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fileConfig{}, fmt.Errorf("decode config file: trailing content")
	}
	return result, nil
}

func boolText(value *bool) string {
	if value == nil {
		return ""
	}
	return strconv.FormatBool(*value)
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
