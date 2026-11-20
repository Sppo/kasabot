package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kasabot/internal/core/apperr"
)

const (
	maxResponseBodyBytes = 2 * 1024 * 1024 // 2 МБ
	maxErrorBodyLogBytes = 256
)

type geminiRequest struct {
	SystemInstruction *geminiContent        `json:"systemInstruction"`
	Contents          []geminiContent       `json:"contents"`
	GenerationConfig  *geminiGenerationConf `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiGenerationConf struct {
	ResponseMimeType string         `json:"responseMimeType"`
	ResponseSchema   map[string]any `json:"responseSchema"`
}

type geminiResponse struct {
	Candidates     []geminiCandidate     `json:"candidates"`
	PromptFeedback *geminiPromptFeedback `json:"promptFeedback"`
	UsageMetadata  *geminiUsageMetadata  `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      *geminiContent `json:"content"`
	FinishReason string         `json:"finishReason"`
}

type geminiPromptFeedback struct {
	BlockReason string `json:"blockReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// Parse виконує валідацію входу, формує захищений запит до Gemini API та повертає валідний JSON.
func (c *GeminiClient) Parse(ctx context.Context, req Request) (Result, error) {
	normalizedMime, err := validateAndNormalizeRequest(req, c.cfg.MaxAudioSizeBytes)
	if err != nil {
		return Result{}, err
	}

	if strings.TrimSpace(c.cfg.GeminiAPIKey) == "" {
		c.logFailure(ctx, apperr.ErrAIUnavailable, "gemini_api_key_missing", 0, len(req.Audio), 0, "")
		return Result{}, apperr.NewAppError(http.StatusServiceUnavailable, apperr.ErrAIUnavailable, "AI service is not configured", nil)
	}

	payload, err := c.buildPayload(req, normalizedMime)
	if err != nil {
		c.logFailure(ctx, apperr.ErrInternal, "gemini_marshal_failed", 0, len(req.Audio), 0, err.Error())
		return Result{}, apperr.NewAppError(http.StatusInternalServerError, apperr.ErrInternal, "failed to prepare AI request", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.baseURL, url.PathEscape(c.cfg.GeminiModel))

	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.AIRequestTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		c.logFailure(ctx, apperr.ErrAIUnavailable, "gemini_build_request_failed", 0, len(req.Audio), 0, err.Error())
		return Result{}, apperr.NewAppError(http.StatusServiceUnavailable, apperr.ErrAIUnavailable, "AI service unavailable", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.cfg.GeminiAPIKey)

	started := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	durationMs := time.Since(started).Milliseconds()
	if err != nil {
		c.logFailure(ctx, apperr.ErrAIUnavailable, "gemini_request_failed", durationMs, len(req.Audio), 0, err.Error())
		return Result{}, apperr.NewAppError(http.StatusServiceUnavailable, apperr.ErrAIUnavailable, "AI service unavailable", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if readErr != nil {
		c.logFailure(ctx, apperr.ErrAIUnavailable, "gemini_read_body_failed", durationMs, len(req.Audio), resp.StatusCode, readErr.Error())
		return Result{}, apperr.NewAppError(http.StatusServiceUnavailable, apperr.ErrAIUnavailable, "AI service unavailable", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, c.mapUpstreamError(ctx, resp.StatusCode, durationMs, len(req.Audio), bodyBytes)
	}

	var parsed geminiResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		c.logFailure(ctx, apperr.ErrAIParseFailed, "gemini_response_decode_failed", durationMs, len(req.Audio), resp.StatusCode, err.Error())
		return Result{}, apperr.NewAppError(http.StatusUnprocessableEntity, apperr.ErrAIParseFailed, "failed to parse AI response", err)
	}

	rawJSON, err := extractValidJSON(parsed)
	if err != nil {
		c.logFailure(ctx, apperr.ErrAIParseFailed, "gemini_invalid_output", durationMs, len(req.Audio), resp.StatusCode, err.Error())
		return Result{}, apperr.NewAppError(http.StatusUnprocessableEntity, apperr.ErrAIParseFailed, "AI returned invalid or blocked response", err)
	}

	c.logSuccess(ctx, durationMs, len(req.Audio), resp.StatusCode, parsed.UsageMetadata)
	return Result{JSON: rawJSON}, nil
}

func (c *GeminiClient) buildPayload(req Request, normalizedMime string) ([]byte, error) {
	loc := c.cfg.DefaultLocation
	if loc == nil {
		loc = time.UTC
	}
	nowStr := c.now().In(loc).Format(time.RFC3339)
	sysPrompt := fmt.Sprintf("%s\n\nCurrent date and time (%s): %s.", strings.TrimSpace(req.SystemPrompt), loc.String(), nowStr)

	var userPart geminiPart
	if len(req.Audio) > 0 {
		userPart = geminiPart{
			InlineData: &geminiInlineData{
				MimeType: normalizedMime,
				Data:     base64.StdEncoding.EncodeToString(req.Audio),
			},
		}
	} else {
		userPart = geminiPart{Text: req.Text}
	}

	payload := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: sysPrompt}},
		},
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: []geminiPart{userPart},
			},
		},
		GenerationConfig: &geminiGenerationConf{
			ResponseMimeType: "application/json",
			ResponseSchema:   req.ResponseSchema,
		},
	}

	return json.Marshal(payload)
}

func extractValidJSON(resp geminiResponse) (json.RawMessage, error) {
	if resp.PromptFeedback != nil && strings.TrimSpace(resp.PromptFeedback.BlockReason) != "" {
		return nil, fmt.Errorf("prompt blocked: %s", resp.PromptFeedback.BlockReason)
	}
	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("empty candidates")
	}

	cand := resp.Candidates[0]
	if cand.FinishReason != "" && cand.FinishReason != "STOP" {
		return nil, fmt.Errorf("unexpected finishReason: %s", cand.FinishReason)
	}
	if cand.Content == nil || len(cand.Content.Parts) == 0 {
		return nil, fmt.Errorf("empty candidate content")
	}

	var b strings.Builder
	for _, part := range cand.Content.Parts {
		b.WriteString(part.Text)
	}
	raw := strings.TrimSpace(b.String())
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("candidate output is not valid JSON")
	}

	return json.RawMessage(raw), nil
}

func (c *GeminiClient) logSuccess(ctx context.Context, durationMs int64, audioBytes int, status int, usage *geminiUsageMetadata) {
	if c.logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("model", c.cfg.GeminiModel),
		slog.Int("status", status),
		slog.Int64("duration_ms", durationMs),
		slog.Int("audio_bytes", audioBytes),
	}
	if usage != nil {
		attrs = append(attrs,
			slog.Int("prompt_tokens", usage.PromptTokenCount),
			slog.Int("candidates_tokens", usage.CandidatesTokenCount),
			slog.Int("total_tokens", usage.TotalTokenCount),
		)
	}
	c.logger.Slog().LogAttrs(ctx, slog.LevelInfo, "gemini_parse_succeeded", attrs...)
}

func (c *GeminiClient) logFailure(ctx context.Context, code apperr.ErrorCode, msg string, durationMs int64, audioBytes int, status int, detail string) {
	if c.logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("model", c.cfg.GeminiModel),
		slog.Int64("duration_ms", durationMs),
		slog.Int("audio_bytes", audioBytes),
	}
	if status > 0 {
		attrs = append(attrs, slog.Int("status", status))
	}
	if detail != "" {
		attrs = append(attrs, slog.String("detail", detail))
	}
	c.logger.LogError(ctx, code, msg, attrs...)
}

func (c *GeminiClient) mapUpstreamError(ctx context.Context, statusCode int, durationMs int64, audioBytes int, bodyBytes []byte) error {
	truncated := truncateString(string(bodyBytes), maxErrorBodyLogBytes)
	switch {
	case statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError:
		c.logFailure(ctx, apperr.ErrAIUnavailable, "gemini_upstream_error", durationMs, audioBytes, statusCode, truncated)
		return apperr.NewAppError(http.StatusServiceUnavailable, apperr.ErrAIUnavailable, "AI service unavailable", nil)
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		c.logFailure(ctx, apperr.ErrInternal, "gemini_auth_failed", durationMs, audioBytes, statusCode, truncated)
		return apperr.NewAppError(http.StatusInternalServerError, apperr.ErrInternal, "AI service configuration error", nil)
	default:
		// 400/404/422 та інші неповторювані клієнтські помилки не повинні ретраїтись офлайн-чергою.
		c.logFailure(ctx, apperr.ErrAIParseFailed, "gemini_request_rejected", durationMs, audioBytes, statusCode, truncated)
		return apperr.NewAppError(http.StatusUnprocessableEntity, apperr.ErrAIParseFailed, "AI request rejected", nil)
	}
}

func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "..."
}
