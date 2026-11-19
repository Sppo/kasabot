package core

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Значення за замовчуванням для операційних параметрів (перевизначаються через .env або змінні ОС).
const (
	defaultPort                   = 8080
	defaultDBMaxConns             = 10
	defaultDBConnectTimeoutSec    = 5
	defaultHTTPTimeoutSec         = 30
	defaultMaxJSONBodyBytes       = 1024 * 1024 // 1 МБ
	defaultJWTTTLHours            = 168         // 7 діб
	defaultGeminiModel            = "gemini-2.5-flash"
	defaultAIRequestTimeoutSec    = 25
	defaultMaxAudioSizeBytes      = 3 * 1024 * 1024 // 3 МБ
	defaultTrialDays              = 14
	defaultTrialDailyAILimit      = 15
	defaultActiveDailyAILimit     = 60
	defaultWorkspaceInviteTTLHour = 48
	defaultTGLinkTokenTTLMin      = 10
	defaultTimezone               = "Europe/Kyiv"
	defaultCurrency               = "UAH"
	defaultLogDir                 = "logs"
	defaultLogRetentionDays       = 0

	minJWTSecretLength     = 16
	insecureJWTPlaceholder = "change-me-in-production-min-16-chars"
)

// Config зберігає всі операційні налаштування програми, зчитані з .env або змінних середовища.
type Config struct {
	Port               int
	DatabaseURL        string
	DBMaxConns         int32
	DBConnectTimeout   time.Duration
	HTTPTimeout        time.Duration
	MaxJSONBodyBytes   int64
	JWTSecret          string
	JWTTTL             time.Duration
	GeminiAPIKey       string
	GeminiModel        string
	AIRequestTimeout   time.Duration
	MaxAudioSizeBytes  int64
	TrialDays          int
	TrialDailyAILimit  int
	ActiveDailyAILimit int
	WorkspaceInviteTTL time.Duration
	TGBotToken         string
	TGLinkTokenTTL     time.Duration
	DefaultTimezone    string
	DefaultLocation    *time.Location
	DefaultCurrency    string
	LogDir             string
	LogRetentionDays   int
}

// LoadConfig завантажує змінні з .env файлу (у локальну мапу без мутації os.Setenv) та оточення ОС, підставляє дефолти й валідує значення.
func LoadConfig(envFilePath string) (*Config, error) {
	var dotEnv map[string]string
	if envFilePath != "" {
		parsed, err := parseDotEnvFile(envFilePath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read env file %s: %w", envFilePath, err)
		}
		dotEnv = parsed
	}

	port, err := envIntRange(dotEnv, "PORT", defaultPort, 1, 65535)
	if err != nil {
		return nil, err
	}

	databaseURL := envString(dotEnv, "DATABASE_URL", "")
	if databaseURL == "" {
		return nil, fmt.Errorf("invalid DATABASE_URL: must be explicitly set")
	}

	dbMaxConns, err := envPositiveInt(dotEnv, "DB_MAX_CONNS", defaultDBMaxConns)
	if err != nil {
		return nil, err
	}

	dbConnectTimeoutSec, err := envPositiveInt(dotEnv, "DB_CONNECT_TIMEOUT_SEC", defaultDBConnectTimeoutSec)
	if err != nil {
		return nil, err
	}

	httpTimeoutSec, err := envPositiveInt(dotEnv, "HTTP_TIMEOUT_SEC", defaultHTTPTimeoutSec)
	if err != nil {
		return nil, err
	}

	maxJSONBytes, err := envPositiveInt64(dotEnv, "MAX_JSON_BODY_BYTES", defaultMaxJSONBodyBytes)
	if err != nil {
		return nil, err
	}

	jwtTTLHours, err := envPositiveInt(dotEnv, "JWT_TTL_HOURS", defaultJWTTTLHours)
	if err != nil {
		return nil, err
	}

	aiTimeoutSec, err := envPositiveInt(dotEnv, "AI_REQUEST_TIMEOUT_SEC", defaultAIRequestTimeoutSec)
	if err != nil {
		return nil, err
	}

	maxAudioBytes, err := envPositiveInt64(dotEnv, "MAX_AUDIO_SIZE_BYTES", defaultMaxAudioSizeBytes)
	if err != nil {
		return nil, err
	}

	trialDays, err := envPositiveInt(dotEnv, "TRIAL_DAYS", defaultTrialDays)
	if err != nil {
		return nil, err
	}

	trialAILimit, err := envPositiveInt(dotEnv, "TRIAL_DAILY_AI_LIMIT", defaultTrialDailyAILimit)
	if err != nil {
		return nil, err
	}

	activeAILimit, err := envPositiveInt(dotEnv, "ACTIVE_DAILY_AI_LIMIT", defaultActiveDailyAILimit)
	if err != nil {
		return nil, err
	}

	inviteTTLHours, err := envPositiveInt(dotEnv, "WORKSPACE_INVITE_TTL_HOURS", defaultWorkspaceInviteTTLHour)
	if err != nil {
		return nil, err
	}

	tgLinkTTLMin, err := envPositiveInt(dotEnv, "TG_LINK_TOKEN_TTL_MIN", defaultTGLinkTokenTTLMin)
	if err != nil {
		return nil, err
	}

	tzName := envString(dotEnv, "DEFAULT_TIMEZONE", defaultTimezone)
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("invalid DEFAULT_TIMEZONE %q: %w", tzName, err)
	}

	jwtSecret := envString(dotEnv, "JWT_SECRET", "")
	if len(jwtSecret) < minJWTSecretLength || jwtSecret == insecureJWTPlaceholder {
		return nil, fmt.Errorf("invalid JWT_SECRET: must be explicitly set to a secret of at least %d characters (placeholder is not allowed)", minJWTSecretLength)
	}

	currency := strings.ToUpper(envString(dotEnv, "DEFAULT_CURRENCY", defaultCurrency))
	if !isValidCurrencyCode(currency) {
		return nil, fmt.Errorf("invalid DEFAULT_CURRENCY %q: must be 3 uppercase Latin letters (A-Z)", currency)
	}

	logDir := envString(dotEnv, "LOG_DIR", defaultLogDir)
	logRetentionDays, err := envNonNegativeInt(dotEnv, "LOG_RETENTION_DAYS", defaultLogRetentionDays)
	if err != nil {
		return nil, err
	}

	return &Config{
		Port:               port,
		DatabaseURL:        databaseURL,
		DBMaxConns:         int32(dbMaxConns),
		DBConnectTimeout:   time.Duration(dbConnectTimeoutSec) * time.Second,
		HTTPTimeout:        time.Duration(httpTimeoutSec) * time.Second,
		MaxJSONBodyBytes:   maxJSONBytes,
		JWTSecret:          jwtSecret,
		JWTTTL:             time.Duration(jwtTTLHours) * time.Hour,
		GeminiAPIKey:       envString(dotEnv, "GEMINI_API_KEY", ""),
		GeminiModel:        envString(dotEnv, "GEMINI_MODEL", defaultGeminiModel),
		AIRequestTimeout:   time.Duration(aiTimeoutSec) * time.Second,
		MaxAudioSizeBytes:  maxAudioBytes,
		TrialDays:          trialDays,
		TrialDailyAILimit:  trialAILimit,
		ActiveDailyAILimit: activeAILimit,
		WorkspaceInviteTTL: time.Duration(inviteTTLHours) * time.Hour,
		TGBotToken:         envString(dotEnv, "TG_BOT_TOKEN", ""),
		TGLinkTokenTTL:     time.Duration(tgLinkTTLMin) * time.Minute,
		DefaultTimezone:    tzName,
		DefaultLocation:    loc,
		DefaultCurrency:    currency,
		LogDir:             logDir,
		LogRetentionDays:   logRetentionDays,
	}, nil
}

// isValidCurrencyCode перевіряє, що код валюти складається рівно з 3 латинських літер A-Z.
func isValidCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}
	return true
}

// parseDotEnvFile зчитує пари KEY=VALUE з файлу .env у локальну мапу без зміни глобального оточення ОС.
func parseDotEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}

		key, rawVal, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		val, err := parseDotEnvValue(strings.TrimSpace(rawVal))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", lineNum, key, err)
		}
		values[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// parseDotEnvValue обробляє значення з .env: збалансовані лапки та inline-коментарі (# ...).
func parseDotEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	first := raw[0]
	if first == '"' || first == '\'' {
		closeIdx := strings.IndexByte(raw[1:], first)
		if closeIdx == -1 {
			return "", fmt.Errorf("unbalanced quote %c in value", first)
		}
		closeIdx++
		val := raw[1:closeIdx]
		rest := strings.TrimSpace(raw[closeIdx+1:])
		if rest != "" && !strings.HasPrefix(rest, "#") {
			return "", fmt.Errorf("unexpected trailing characters after closing quote")
		}
		return val, nil
	}

	if strings.HasPrefix(raw, "#") {
		return "", nil
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i > 0 && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			raw = strings.TrimSpace(raw[:i])
			break
		}
	}

	return raw, nil
}

func lookupConfigValue(dotEnv map[string]string, key string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	if dotEnv != nil {
		if val := strings.TrimSpace(dotEnv[key]); val != "" {
			return val
		}
	}
	return ""
}

func envString(dotEnv map[string]string, key, fallback string) string {
	val := lookupConfigValue(dotEnv, key)
	if val == "" {
		return fallback
	}
	return val
}

func envPositiveInt(dotEnv map[string]string, key string, fallback int) (int, error) {
	raw := lookupConfigValue(dotEnv, key)
	if raw == "" {
		return fallback, nil
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be an integer: %w", key, raw, err)
	}
	if val <= 0 {
		return 0, fmt.Errorf("invalid %s=%d: must be greater than 0", key, val)
	}
	return val, nil
}

func envIntRange(dotEnv map[string]string, key string, fallback, minVal, maxVal int) (int, error) {
	raw := lookupConfigValue(dotEnv, key)
	if raw == "" {
		return fallback, nil
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be an integer: %w", key, raw, err)
	}
	if val < minVal || val > maxVal {
		return 0, fmt.Errorf("invalid %s=%d: must be between %d and %d", key, val, minVal, maxVal)
	}
	return val, nil
}

func envNonNegativeInt(dotEnv map[string]string, key string, fallback int) (int, error) {
	raw := lookupConfigValue(dotEnv, key)
	if raw == "" {
		return fallback, nil
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be an integer: %w", key, raw, err)
	}
	if val < 0 {
		return 0, fmt.Errorf("invalid %s=%d: must be greater than or equal to 0", key, val)
	}
	return val, nil
}

func envPositiveInt64(dotEnv map[string]string, key string, fallback int64) (int64, error) {
	raw := lookupConfigValue(dotEnv, key)
	if raw == "" {
		return fallback, nil
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be an integer: %w", key, raw, err)
	}
	if val <= 0 {
		return 0, fmt.Errorf("invalid %s=%d: must be greater than 0", key, val)
	}
	return val, nil
}
