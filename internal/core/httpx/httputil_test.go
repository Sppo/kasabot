package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/reqctx"
)

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
	if _, ok := reqctx.UserIDFromCtx(ctx); ok {
		t.Fatal("expected no user ID in empty context")
	}

	ctx = reqctx.WithUserID(ctx, 42)
	ctx = reqctx.WithWorkspaceID(ctx, 99)

	uid, ok := reqctx.UserIDFromCtx(ctx)
	if !ok || uid != 42 {
		t.Fatalf("expected userID=42, got %d (ok=%v)", uid, ok)
	}

	wid, ok := reqctx.WorkspaceIDFromCtx(ctx)
	if !ok || wid != 99 {
		t.Fatalf("expected workspaceID=99, got %d (ok=%v)", wid, ok)
	}

	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusPaymentRequired, apperr.ErrSubscriptionExpired, "trial ended")

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.ErrorCode != apperr.ErrSubscriptionExpired {
		t.Errorf("expected error_code=%q, got %q", apperr.ErrSubscriptionExpired, resp.ErrorCode)
	}
}
