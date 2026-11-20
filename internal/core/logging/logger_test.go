package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/config"
	"kasabot/internal/core/reqctx"
)

func newTestLoggerConfig(logDir string, retentionDays int) *config.Config {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	return &config.Config{
		LogDir:           logDir,
		LogRetentionDays: retentionDays,
		DefaultLocation:  loc,
	}
}

func TestLoggerDailyFilesErrorIDAndHook(t *testing.T) {
	logDir := t.TempDir()
	var stdoutBuf bytes.Buffer

	fixedNow := time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC)
	logger, err := NewLogger(
		newTestLoggerConfig(logDir, 0),
		WithLoggerClock(func() time.Time { return fixedNow }),
		WithLoggerStdout(&stdoutBuf),
	)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	var hookedEvent ErrorEvent
	hookCalled := false
	logger.SetErrorHook(func(_ context.Context, ev ErrorEvent) {
		hookCalled = true
		hookedEvent = ev
	})

	ctx := reqctx.WithRequestID(context.Background(), "req-abc-123")
	ctx = reqctx.WithWorkspaceID(ctx, 77)
	ctx = reqctx.WithUserID(ctx, 12)

	// Перевіряємо, що порожній errors-файл не створюється, доки не було першої помилки
	logger.Slog().InfoContext(ctx, "normal_event", slog.String("action", "ping"))
	if _, statErr := os.Stat(filepath.Join(logDir, "errors-2026-11-20.log")); !os.IsNotExist(statErr) {
		t.Fatalf("expected errors log NOT to be created before first ERROR")
	}

	errID := logger.LogError(ctx, apperr.ErrInternal, "database failure")

	if len(errID) != 8 {
		t.Fatalf("expected 8-char hex error_id, got %q", errID)
	}
	if !hookCalled || hookedEvent.ErrorID != errID || hookedEvent.ErrorCode != apperr.ErrInternal {
		t.Fatalf("expected ErrorHook to be called with error_id=%q and code=%q, got %+v", errID, apperr.ErrInternal, hookedEvent)
	}

	appLogBytes, err := os.ReadFile(filepath.Join(logDir, "app-2026-11-20.log"))
	if err != nil {
		t.Fatalf("read app log: %v", err)
	}
	errLogBytes, err := os.ReadFile(filepath.Join(logDir, "errors-2026-11-20.log"))
	if err != nil {
		t.Fatalf("read errors log: %v", err)
	}

	appLog := string(appLogBytes)
	errLog := string(errLogBytes)

	if !strings.Contains(appLog, "normal_event") || !strings.Contains(appLog, errID) {
		t.Errorf("expected app log to contain both normal_event and error_id %q, got:\n%s", errID, appLog)
	}
	if strings.Contains(errLog, "normal_event") {
		t.Errorf("expected errors log NOT to contain INFO normal_event, got:\n%s", errLog)
	}
	if !strings.Contains(errLog, errID) || !strings.Contains(errLog, "req-abc-123") || !strings.Contains(errLog, string(apperr.ErrInternal)) {
		t.Errorf("expected errors log to contain error_id, request_id and error_code, got:\n%s", errLog)
	}
	if !strings.Contains(stdoutBuf.String(), errID) {
		t.Errorf("expected stdout to mirror logs with error_id %q", errID)
	}
}

func TestLoggerDayRolloverAndRetention(t *testing.T) {
	logDir := t.TempDir()
	loc, _ := time.LoadLocation("Europe/Kyiv")

	// Попередньо створюємо старі файли: один старіший за 14 днів, один свіжіший
	oldApp := filepath.Join(logDir, "app-2026-11-01.log")
	oldErr := filepath.Join(logDir, "errors-2026-11-01.log")
	recentApp := filepath.Join(logDir, "app-2026-11-15.log")
	for _, p := range []string{oldApp, oldErr, recentApp} {
		if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
			t.Fatalf("write seed log %s: %v", p, err)
		}
	}

	currentTime := time.Date(2026, 11, 20, 23, 50, 0, 0, loc)
	logger, err := NewLogger(
		newTestLoggerConfig(logDir, 14),
		WithLoggerClock(func() time.Time { return currentTime }),
		WithLoggerStdout(nil),
	)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	// Старі файли (2026-11-01 < 2026-11-06) мають бути видалені, а 2026-11-15 — збережений
	if _, err := os.Stat(oldApp); !os.IsNotExist(err) {
		t.Errorf("expected old app log %s to be deleted", oldApp)
	}
	if _, err := os.Stat(oldErr); !os.IsNotExist(err) {
		t.Errorf("expected old errors log %s to be deleted", oldErr)
	}
	if _, err := os.Stat(recentApp); err != nil {
		t.Errorf("expected recent log %s to be kept, got: %v", recentApp, err)
	}

	// Перемикаємо годинник на наступний день і пишемо подію з новим Time у Record через slog
	nextDay := time.Date(2026, 11, 21, 0, 5, 0, 0, loc)
	currentTime = nextDay
	rec := slog.NewRecord(nextDay, slog.LevelError, "next_day_error", 0)
	rec.AddAttrs(slog.String("error_id", "deadbeef"))
	if err := logger.Slog().Handler().Handle(context.Background(), rec); err != nil {
		t.Fatalf("handle next day record: %v", err)
	}

	if _, err := os.Stat(filepath.Join(logDir, "app-2026-11-21.log")); err != nil {
		t.Errorf("expected next day app log to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(logDir, "errors-2026-11-21.log")); err != nil {
		t.Errorf("expected next day errors log to be created: %v", err)
	}
}

func TestLoggerWithAttrsWithGroupAndCloneRollover(t *testing.T) {
	logDir := t.TempDir()
	var stdoutBuf bytes.Buffer
	loc, _ := time.LoadLocation("Europe/Kyiv")
	day1 := time.Date(2026, 11, 20, 12, 0, 0, 0, loc)

	logger, err := NewLogger(
		newTestLoggerConfig(logDir, 0),
		WithLoggerClock(func() time.Time { return day1 }),
		WithLoggerStdout(&stdoutBuf),
	)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	// Створюємо клон через With та WithGroup
	child := logger.Slog().With("k", "v").WithGroup("g")

	recInfo := slog.NewRecord(day1, slog.LevelInfo, "child_info", 0)
	recInfo.AddAttrs(slog.String("inner", "123"))
	if err := child.Handler().Handle(context.Background(), recInfo); err != nil {
		t.Fatalf("child INFO handle: %v", err)
	}

	recErr := slog.NewRecord(day1, slog.LevelError, "child_err", 0)
	recErr.AddAttrs(slog.String("inner", "456"))
	if err := child.Handler().Handle(context.Background(), recErr); err != nil {
		t.Fatalf("child ERROR handle: %v", err)
	}

	appDay1, err := os.ReadFile(filepath.Join(logDir, "app-2026-11-20.log"))
	if err != nil {
		t.Fatalf("read day1 app log: %v", err)
	}
	errDay1, err := os.ReadFile(filepath.Join(logDir, "errors-2026-11-20.log"))
	if err != nil {
		t.Fatalf("read day1 errors log: %v", err)
	}

	if !strings.Contains(string(appDay1), `"k":"v"`) || !strings.Contains(string(appDay1), `"g":{"inner":"123"}`) {
		t.Errorf("expected app log to contain With/WithGroup attributes, got:\n%s", string(appDay1))
	}
	if !strings.Contains(string(errDay1), `"k":"v"`) || !strings.Contains(string(errDay1), `"g":{"inner":"456"}`) {
		t.Errorf("expected errors log to contain With/WithGroup attributes, got:\n%s", string(errDay1))
	}
	if !strings.Contains(stdoutBuf.String(), `"k":"v"`) {
		t.Errorf("expected stdout to contain With attribute, got:\n%s", stdoutBuf.String())
	}

	// Перехід доби: і батько, і клон пишуть у новий файл без втрати рядків
	day2 := time.Date(2026, 11, 21, 0, 10, 0, 0, loc)
	recChildDay2 := slog.NewRecord(day2, slog.LevelInfo, "child_day2", 0)
	if err := child.Handler().Handle(context.Background(), recChildDay2); err != nil {
		t.Fatalf("child day2 handle: %v", err)
	}
	recParentDay2 := slog.NewRecord(day2, slog.LevelInfo, "parent_day2", 0)
	if err := logger.Slog().Handler().Handle(context.Background(), recParentDay2); err != nil {
		t.Fatalf("parent day2 handle: %v", err)
	}

	appDay2, err := os.ReadFile(filepath.Join(logDir, "app-2026-11-21.log"))
	if err != nil {
		t.Fatalf("read day2 app log: %v", err)
	}
	if !strings.Contains(string(appDay2), "child_day2") || !strings.Contains(string(appDay2), "parent_day2") {
		t.Errorf("expected both child_day2 and parent_day2 in day2 app log, got:\n%s", string(appDay2))
	}
}

func TestRequestLoggingMiddlewareNoSecretsAndCapturesRequestID(t *testing.T) {
	logDir := t.TempDir()
	var stdoutBuf bytes.Buffer
	loc, _ := time.LoadLocation("Europe/Kyiv")
	fixedNow := time.Date(2026, 11, 20, 15, 0, 0, 0, loc)

	logger, err := NewLogger(
		newTestLoggerConfig(logDir, 0),
		WithLoggerClock(func() time.Time { return fixedNow }),
		WithLoggerStdout(&stdoutBuf),
	)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	mw := RequestLoggingMiddleware(logger)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := reqctx.WithUserID(r.Context(), 5)
		ctx = reqctx.WithWorkspaceID(ctx, 10)
		// Імітуємо помилку сервера з error_id у JSON-відповіді
		errID := logger.LogError(ctx, apperr.ErrInternal, "failed_op")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error_code": string(apperr.ErrInternal),
			"error_id":   errID,
			"message":    "internal error",
		})
	}))

	reqBody := `{"password":"super-secret-password-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer top-secret-jwt-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	reqID := rec.Header().Get("X-Request-ID")
	if reqID == "" {
		t.Fatal("expected X-Request-ID header in response")
	}

	var errResp struct {
		ErrorCode apperr.ErrorCode `json:"error_code"`
		ErrorID   string           `json:"error_id"`
		Message   string           `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if errResp.ErrorID == "" || errResp.ErrorCode != apperr.ErrInternal {
		t.Fatalf("expected error_id and error_code in JSON response, got %+v", errResp)
	}

	logOutput := stdoutBuf.String()
	if !strings.Contains(logOutput, reqID) || !strings.Contains(logOutput, errResp.ErrorID) {
		t.Errorf("expected log to contain request_id %q and error_id %q", reqID, errResp.ErrorID)
	}
	if strings.Contains(logOutput, "super-secret-password-123") || strings.Contains(logOutput, "top-secret-jwt-token") {
		t.Fatalf("SECURITY VIOLATION: secret password or JWT token leaked into logs:\n%s", logOutput)
	}

	// Перевіряємо, що без workspace_id / user_id у контексті зайві нулі не пишуться в лог
	stdoutBuf.Reset()
	noCtxReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	noCtxRec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(noCtxRec, noCtxReq)

	if strings.Contains(stdoutBuf.String(), `"workspace_id":0`) || strings.Contains(stdoutBuf.String(), `"user_id":0`) {
		t.Errorf("expected workspace_id/user_id to be omitted when absent from context, got:\n%s", stdoutBuf.String())
	}
}
