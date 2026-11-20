package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/config"
	"kasabot/internal/core/logging"
)

const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"

// Білий список дозволених аудіо MIME-типів згідно з ARCHITECTURE_RULES.md.
var allowedAudioMimeTypes = map[string]bool{
	"audio/ogg":   true,
	"audio/webm":  true,
	"audio/mp4":   true,
	"audio/mpeg":  true,
	"audio/wav":   true,
	"audio/aac":   true,
	"audio/x-m4a": true,
}

// Client визначає універсальний контракт AI-парсера для всіх модулів системи.
type Client interface {
	Parse(ctx context.Context, req Request) (Result, error)
}

// Request описує вхідні дані для розбору тексту або аудіо через AI.
type Request struct {
	SystemPrompt   string
	Text           string
	Audio          []byte
	MimeType       string
	ResponseSchema map[string]any
}

// Result містить валідний JSON, повернутий моделлю відповідно до ResponseSchema.
type Result struct {
	JSON json.RawMessage
}

// GeminiOption налаштовує GeminiClient (зокрема для підміни URL, HTTP-клієнта або годинника в тестах).
type GeminiOption func(*GeminiClient)

// WithBaseURL задає базовий URL Gemini API (у тестах — URL httptest.Server).
func WithBaseURL(baseURL string) GeminiOption {
	return func(c *GeminiClient) {
		trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if trimmed != "" {
			c.baseURL = trimmed
		}
	}
}

// WithHTTPClient підміняє HTTP-клієнт (корисно для тестів або кастомного транспорту).
func WithHTTPClient(httpClient *http.Client) GeminiOption {
	return func(c *GeminiClient) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithClock задає функцію поточного часу (корисно для детермінованих тестів системного промпту).
func WithClock(now func() time.Time) GeminiOption {
	return func(c *GeminiClient) {
		if now != nil {
			c.now = now
		}
	}
}

// GeminiClient реалізує інтерфейс Client поверх REST API Google Gemini.
type GeminiClient struct {
	cfg        *config.Config
	logger     *logging.Logger
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

// NewGeminiClient створює клієнт Gemini API з операційними параметрами з Config.
func NewGeminiClient(cfg *config.Config, logger *logging.Logger, opts ...GeminiOption) (*GeminiClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("new gemini client: config is nil")
	}
	if strings.TrimSpace(cfg.GeminiModel) == "" {
		return nil, fmt.Errorf("new gemini client: gemini model is empty")
	}
	if cfg.AIRequestTimeout <= 0 {
		return nil, fmt.Errorf("new gemini client: AIRequestTimeout must be positive")
	}
	if cfg.MaxAudioSizeBytes <= 0 {
		return nil, fmt.Errorf("new gemini client: MaxAudioSizeBytes must be positive")
	}

	c := &GeminiClient{
		cfg:        cfg,
		logger:     logger,
		baseURL:    defaultGeminiBaseURL,
		httpClient: &http.Client{},
		now:        time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

// validateAndNormalizeRequest перевіряє коректність вхідного запиту до виконання мережевого виклику.
func validateAndNormalizeRequest(req Request, maxAudioBytes int64) (string, error) {
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return "", apperr.NewAppError(http.StatusBadRequest, apperr.ErrValidation, "system prompt is required", nil)
	}
	if len(req.ResponseSchema) == 0 {
		return "", apperr.NewAppError(http.StatusBadRequest, apperr.ErrValidation, "response schema is required", nil)
	}

	hasText := strings.TrimSpace(req.Text) != ""
	hasAudio := len(req.Audio) > 0
	if hasText == hasAudio {
		return "", apperr.NewAppError(http.StatusBadRequest, apperr.ErrValidation, "exactly one of text or audio must be provided", nil)
	}

	if !hasAudio {
		return "", nil
	}

	if int64(len(req.Audio)) > maxAudioBytes {
		return "", apperr.NewAppError(http.StatusRequestEntityTooLarge, apperr.ErrAudioTooLarge, "audio file exceeds maximum allowed size", nil)
	}

	mimeType := normalizeMimeType(req.MimeType)
	if !allowedAudioMimeTypes[mimeType] {
		return "", apperr.NewAppError(http.StatusUnsupportedMediaType, apperr.ErrUnsupportedMedia, "unsupported audio media type", nil)
	}

	return mimeType, nil
}

// normalizeMimeType приводить MIME-тип до нижнього регістру та відкидає параметри (наприклад, ";codecs=opus").
func normalizeMimeType(raw string) string {
	base, _, _ := strings.Cut(raw, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
