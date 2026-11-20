package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigDefaultsAndEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")

	envContent := strings.Join([]string{
		"# Тестовий .env файл з коментарями, export та лапками",
		"PORT=9090 # inline коментар після числа",
		"export DATABASE_URL=\"postgres://kasabot:pass@127.0.0.1:5432/kasabot?sslmode=disable\" # коментар після лапок",
		"DB_MAX_CONNS=15",
		"DB_CONNECT_TIMEOUT_SEC=8",
		"JWT_SECRET='super-secret-jwt-key-12345#with-hash'",
		"TRIAL_DAYS=7",
		"DEFAULT_TIMEZONE=Europe/Kyiv",
		"DEFAULT_CURRENCY=uah",
	}, "\n")

	if err := os.WriteFile(envFile, []byte(envContent), 0600); err != nil {
		t.Fatalf("write temp env file: %v", err)
	}

	// Порожні змінні в shell не повинні блокувати значення з .env файлу
	keys := []string{"PORT", "DATABASE_URL", "DB_MAX_CONNS", "DB_CONNECT_TIMEOUT_SEC", "JWT_SECRET", "TRIAL_DAYS", "DEFAULT_TIMEZONE", "DEFAULT_CURRENCY"}
	for _, k := range keys {
		t.Setenv(k, "")
	}

	cfg, err := LoadConfig(envFile)
	if err != nil {
		t.Fatalf("expected LoadConfig to succeed, got: %v", err)
	}

	// Перевіряємо, що LoadConfig не забруднив глобальний os.Getenv
	if got := os.Getenv("DATABASE_URL"); got != "" {
		t.Errorf("expected LoadConfig not to mutate global os.Getenv, got DATABASE_URL=%q", got)
	}

	if cfg.Port != 9090 {
		t.Errorf("expected Port=9090, got %d", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://kasabot:pass@127.0.0.1:5432/kasabot?sslmode=disable" {
		t.Errorf("expected DatabaseURL from .env, got %q", cfg.DatabaseURL)
	}
	if cfg.DBMaxConns != 15 {
		t.Errorf("expected DBMaxConns=15, got %d", cfg.DBMaxConns)
	}
	if cfg.DBConnectTimeout != 8*time.Second {
		t.Errorf("expected DBConnectTimeout=8s, got %v", cfg.DBConnectTimeout)
	}
	if cfg.JWTSecret != "super-secret-jwt-key-12345#with-hash" {
		t.Errorf("expected JWTSecret with inner hash preserved, got %q", cfg.JWTSecret)
	}
	if cfg.TrialDays != 7 {
		t.Errorf("expected TrialDays=7, got %d", cfg.TrialDays)
	}
	if cfg.DefaultCurrency != "UAH" {
		t.Errorf("expected DefaultCurrency=UAH, got %q", cfg.DefaultCurrency)
	}
	if cfg.DefaultLocation == nil || cfg.DefaultLocation.String() != "Europe/Kyiv" {
		t.Errorf("expected DefaultLocation=Europe/Kyiv, got %v", cfg.DefaultLocation)
	}
	if cfg.AIRequestTimeout != 25*time.Second {
		t.Errorf("expected default AIRequestTimeout=25s, got %v", cfg.AIRequestTimeout)
	}
	if cfg.MaxJSONBodyBytes != 1024*1024 {
		t.Errorf("expected default MaxJSONBodyBytes=1048576, got %d", cfg.MaxJSONBodyBytes)
	}
}

func TestLoadConfigValidationErrors(t *testing.T) {
	const testDBURL = "postgres://kasabot:kasabot@127.0.0.1:5432/kasabot?sslmode=disable"

	t.Run("missing database url fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		if _, err := LoadConfig(""); err == nil {
			t.Fatal("expected error when DATABASE_URL is not set, got nil")
		}
	})

	t.Run("missing jwt secret fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "")
		if _, err := LoadConfig(""); err == nil {
			t.Fatal("expected error when JWT_SECRET is not set, got nil")
		}
	})

	t.Run("placeholder jwt secret fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "change-me-in-production-min-16-chars")
		if _, err := LoadConfig(""); err == nil {
			t.Fatal("expected error for placeholder JWT_SECRET, got nil")
		}
	})

	t.Run("short jwt secret fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "too-short")
		if _, err := LoadConfig(""); err == nil {
			t.Fatal("expected error for short JWT_SECRET, got nil")
		}
	})

	t.Run("invalid timezone fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		t.Setenv("DEFAULT_TIMEZONE", "Invalid/Zone_Name")
		if _, err := LoadConfig(""); err == nil {
			t.Fatal("expected error for invalid DEFAULT_TIMEZONE, got nil")
		}
	})

	t.Run("numeric currency code fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		t.Setenv("DEFAULT_TIMEZONE", "Europe/Kyiv")
		t.Setenv("DEFAULT_CURRENCY", "123")
		if _, err := LoadConfig(""); err == nil {
			t.Fatal("expected error for numeric DEFAULT_CURRENCY=123, got nil")
		}
	})

	t.Run("unbalanced quotes in env file fail", func(t *testing.T) {
		tmpDir := t.TempDir()
		envFile := filepath.Join(tmpDir, ".env")
		if err := os.WriteFile(envFile, []byte("JWT_SECRET=\"unclosed-secret-1234567890\n"), 0600); err != nil {
			t.Fatalf("write env file: %v", err)
		}
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "")
		if _, err := LoadConfig(envFile); err == nil {
			t.Fatal("expected error for unbalanced quotes in .env, got nil")
		}
	})

	t.Run("non-numeric port distinguishes parse error", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		t.Setenv("PORT", "abc")
		_, err := LoadConfig("")
		if err == nil || !strings.Contains(err.Error(), `PORT="abc"`) || !strings.Contains(err.Error(), "must be an integer") {
			t.Fatalf("expected integer parse error with raw value, got: %v", err)
		}
	})

	t.Run("out-of-range port distinguishes range error", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		t.Setenv("PORT", "70000")
		_, err := LoadConfig("")
		if err == nil || !strings.Contains(err.Error(), "must be between 1 and 65535") {
			t.Fatalf("expected range error, got: %v", err)
		}
	})

	t.Run("negative log retention days fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		t.Setenv("LOG_RETENTION_DAYS", "-1")
		_, err := LoadConfig("")
		if err == nil || !strings.Contains(err.Error(), "greater than or equal to 0") {
			t.Fatalf("expected >= 0 error for LOG_RETENTION_DAYS=-1, got: %v", err)
		}
	})

	t.Run("out-of-range bcrypt cost fails", func(t *testing.T) {
		t.Setenv("DATABASE_URL", testDBURL)
		t.Setenv("JWT_SECRET", "valid-secret-at-least-16-chars")
		t.Setenv("BCRYPT_COST", "9")
		_, err := LoadConfig("")
		if err == nil || !strings.Contains(err.Error(), "must be between 10 and 14") {
			t.Fatalf("expected range error for BCRYPT_COST=9, got: %v", err)
		}
	})
}

func TestDecodeJSONStrictValidation(t *testing.T) {
	type sampleReq struct {
		Name string `json:"name"`
	}

	t.Run("valid json succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"KasaBot"}`))
		rec := httptest.NewRecorder()
		var dst sampleReq
		if err := DecodeJSON(rec, req, &dst, 1024); err != nil {
			t.Fatalf("expected valid JSON to decode, got: %v", err)
		}
		if dst.Name != "KasaBot" {
			t.Errorf("expected Name=KasaBot, got %q", dst.Name)
		}
	})

	t.Run("unknown field fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"KasaBot","extra":123}`))
		rec := httptest.NewRecorder()
		var dst sampleReq
		if err := DecodeJSON(rec, req, &dst, 1024); err == nil {
			t.Fatal("expected error for unknown JSON field, got nil")
		}
	})

	t.Run("trailing data after json fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"KasaBot"}{"second":true}`))
		rec := httptest.NewRecorder()
		var dst sampleReq
		if err := DecodeJSON(rec, req, &dst, 1024); err == nil {
			t.Fatal("expected error for trailing JSON object, got nil")
		}
	})

	t.Run("oversized body fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"this-body-is-longer-than-10-bytes"}`))
		rec := httptest.NewRecorder()
		var dst sampleReq
		if err := DecodeJSON(rec, req, &dst, 10); err == nil {
			t.Fatal("expected error for oversized request body, got nil")
		}
	})
}

func TestHTTPUtilAndContextHelpers(t *testing.T) {
	ctx := context.Background()
	if _, ok := UserIDFromCtx(ctx); ok {
		t.Fatal("expected no user ID in empty context")
	}

	ctx = WithUserID(ctx, 42)
	ctx = WithWorkspaceID(ctx, 99)

	uid, ok := UserIDFromCtx(ctx)
	if !ok || uid != 42 {
		t.Fatalf("expected userID=42, got %d (ok=%v)", uid, ok)
	}

	wid, ok := WorkspaceIDFromCtx(ctx)
	if !ok || wid != 99 {
		t.Fatalf("expected workspaceID=99, got %d (ok=%v)", wid, ok)
	}

	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusPaymentRequired, ErrSubscriptionExpired, "trial ended")

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.ErrorCode != ErrSubscriptionExpired {
		t.Errorf("expected error_code=%q, got %q", ErrSubscriptionExpired, resp.ErrorCode)
	}
}
