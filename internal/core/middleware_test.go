package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAuthAndWorkspaceMiddleware(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	store := NewPGStore(pool)
	auth, _ := NewAuthService(store, cfg, NewModuleRegistry(), nil)
	wsSvc := NewWorkspaceService(store, cfg, NewModuleRegistry(), nil)
	ctx := context.Background()

	owner, _ := auth.Register(ctx, "mw_owner@example.com", "StrongPass123", "Owner", "WS1")
	partner, _ := auth.Register(ctx, "mw_partner@example.com", "StrongPass123", "Partner", "WS2")
	outsider, _ := auth.Register(ctx, "mw_outsider@example.com", "StrongPass123", "Outsider", "WS3")

	inv, _ := wsSvc.CreateInvite(ctx, owner.User.ID, owner.Workspace.ID)
	_, _ = wsSvc.JoinWorkspace(ctx, partner.User.ID, inv.Code)

	protected := AuthMiddleware(auth, nil)(
		WorkspaceMiddleware(auth, wsSvc, nil)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wsID, _ := WorkspaceIDFromCtx(r.Context())
				WriteJSON(w, http.StatusOK, map[string]any{
					"workspace_id": wsID,
					"role":         WorkspaceRoleFromCtx(r.Context()),
				})
			}),
		),
	)

	// Без токена -> 401
	reqNoTok := httptest.NewRequest(http.MethodGet, "/test", nil)
	recNoTok := httptest.NewRecorder()
	protected.ServeHTTP(recNoTok, reqNoTok)
	if recNoTok.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", recNoTok.Code)
	}

	// Чужий X-Workspace-ID -> 403
	reqForeign := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqForeign.Header.Set("Authorization", "Bearer "+outsider.Token)
	reqForeign.Header.Set("X-Workspace-ID", strconv.FormatInt(owner.Workspace.ID, 10))
	recForeign := httptest.NewRecorder()
	protected.ServeHTTP(recForeign, reqForeign)
	if recForeign.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign workspace, got %d", recForeign.Code)
	}

	// Без заголовка X-Workspace-ID береться active_workspace_id
	reqActive := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqActive.Header.Set("Authorization", "Bearer "+partner.Token)
	recActive := httptest.NewRecorder()
	protected.ServeHTTP(recActive, reqActive)
	if recActive.Code != http.StatusOK {
		t.Fatalf("expected 200 using active workspace, got %d", recActive.Code)
	}

	// RequireOwnerMiddleware: member -> 403, owner -> 200
	ownerOnly := AuthMiddleware(auth, nil)(
		WorkspaceMiddleware(auth, wsSvc, nil)(
			RequireOwnerMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})),
		),
	)

	reqMember := httptest.NewRequest(http.MethodPost, "/owner", nil)
	reqMember.Header.Set("Authorization", "Bearer "+partner.Token)
	reqMember.Header.Set("X-Workspace-ID", strconv.FormatInt(owner.Workspace.ID, 10))
	recMember := httptest.NewRecorder()
	ownerOnly.ServeHTTP(recMember, reqMember)
	if recMember.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for member on owner-only route, got %d", recMember.Code)
	}

	reqOwner := httptest.NewRequest(http.MethodPost, "/owner", nil)
	reqOwner.Header.Set("Authorization", "Bearer "+owner.Token)
	recOwner := httptest.NewRecorder()
	ownerOnly.ServeHTTP(recOwner, reqOwner)
	if recOwner.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner on owner-only route, got %d", recOwner.Code)
	}
}

func TestAuthHandlerEndToEnd(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	store := NewPGStore(pool)
	auth, _ := NewAuthService(store, cfg, NewModuleRegistry(), nil)
	wsSvc := NewWorkspaceService(store, cfg, NewModuleRegistry(), nil)
	router := NewAuthHandler(auth, wsSvc, cfg, nil).Routes()

	// 1. Register
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"email":"e2e@example.com","password":"StrongPass123","name":"E2E","workspace_name":"Main"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", rec.Code, rec.Body.String())
	}
	var regResp AuthResult
	_ = json.NewDecoder(rec.Body).Decode(&regResp)

	// 2. Me
	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+regResp.Token)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me failed: %d %s", meRec.Code, meRec.Body.String())
	}

	// 3. CreateInvite -> Partner Join -> Activate
	invReq := httptest.NewRequest(http.MethodPost, "/workspaces/"+strconv.FormatInt(regResp.Workspace.ID, 10)+"/invites", nil)
	invReq.Header.Set("Authorization", "Bearer "+regResp.Token)
	invRec := httptest.NewRecorder()
	router.ServeHTTP(invRec, invReq)
	if invRec.Code != http.StatusCreated {
		t.Fatalf("create invite failed: %d %s", invRec.Code, invRec.Body.String())
	}
	var invResp WorkspaceInviteResult
	_ = json.NewDecoder(invRec.Body).Decode(&invResp)

	pRec := httptest.NewRecorder()
	router.ServeHTTP(pRec, httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"email":"p2@example.com","password":"StrongPass123","name":"P2"}`)))
	var pResp AuthResult
	_ = json.NewDecoder(pRec.Body).Decode(&pResp)

	joinReq := httptest.NewRequest(http.MethodPost, "/workspaces/join", strings.NewReader(`{"code":"`+invResp.Code+`"}`))
	joinReq.Header.Set("Authorization", "Bearer "+pResp.Token)
	joinRec := httptest.NewRecorder()
	router.ServeHTTP(joinRec, joinReq)
	if joinRec.Code != http.StatusOK {
		t.Fatalf("join failed: %d %s", joinRec.Code, joinRec.Body.String())
	}

	actReq := httptest.NewRequest(http.MethodPost, "/workspaces/"+strconv.FormatInt(pResp.Workspace.ID, 10)+"/activate", nil)
	actReq.Header.Set("Authorization", "Bearer "+pResp.Token)
	actRec := httptest.NewRecorder()
	router.ServeHTTP(actRec, actReq)
	if actRec.Code != http.StatusOK {
		t.Fatalf("activate failed: %d %s", actRec.Code, actRec.Body.String())
	}
}
