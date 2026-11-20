package core

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceCreationLimitAndCompensation(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir()) // MaxWorkspacesPerUser = 2
	store := NewPGStore(pool)

	mod := &seedFailModule{name: "finance", fail: false}
	reg := NewModuleRegistry()
	_ = reg.Register(mod)

	auth, _ := NewAuthService(store, cfg, reg, nil)
	wsSvc := NewWorkspaceService(store, cfg, reg, nil)
	ctx := context.Background()

	regRes, err := auth.Register(ctx, "owner@example.com", "StrongPass123", "Owner", "ФОП")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	firstWSID := regRes.Workspace.ID

	// Якщо сід другого воркспейсу падає — новий воркспейс видаляється, а active_workspace_id не змінюється
	mod.fail = true
	_, err = wsSvc.CreateWorkspace(ctx, regRes.User.ID, "Сім'я", "UAH")
	assertAppErrorCode(t, err, http.StatusInternalServerError, ErrInternal)

	activeID, err := auth.GetActiveWorkspaceID(ctx, regRes.User.ID)
	if err != nil || activeID != firstWSID {
		t.Fatalf("expected active workspace to remain %d, got %d (err=%v)", firstWSID, activeID, err)
	}

	// Успішне створення 2-го воркспейсу (досягаємо ліміту 2)
	mod.fail = false
	ws2, err := wsSvc.CreateWorkspace(ctx, regRes.User.ID, "Сім'я", "USD")
	if err != nil {
		t.Fatalf("CreateWorkspace #2 failed: %v", err)
	}
	if ws2.Currency != "USD" {
		t.Errorf("expected currency USD, got %s", ws2.Currency)
	}

	// Занадто довга назва воркспейсу (> 100) -> 400 ERR_VALIDATION
	_, err = wsSvc.CreateWorkspace(ctx, regRes.User.ID, strings.Repeat("w", 101), "UAH")
	assertAppErrorCode(t, err, http.StatusBadRequest, ErrValidation)

	// Спроба створити 3-й воркспейс при ліміті 2 -> 403 ERR_FORBIDDEN
	_, err = wsSvc.CreateWorkspace(ctx, regRes.User.ID, "Третій", "UAH")
	assertAppErrorCode(t, err, http.StatusForbidden, ErrForbidden)
}

func TestWorkspaceInvitesAndJoin(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	store := NewPGStore(pool)
	auth, _ := NewAuthService(store, cfg, NewModuleRegistry(), nil)
	wsSvc := NewWorkspaceService(store, cfg, NewModuleRegistry(), nil)
	ctx := context.Background()

	owner, _ := auth.Register(ctx, "owner@example.com", "StrongPass123", "Owner", "Сім'я")
	partner, _ := auth.Register(ctx, "partner@example.com", "StrongPass123", "Partner", "Особистий")
	outsider, _ := auth.Register(ctx, "outsider@example.com", "StrongPass123", "Outsider", "Чужий")

	// Партнер приєднується за інвайтом
	inv, err := wsSvc.CreateInvite(ctx, owner.User.ID, owner.Workspace.ID)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	joined, err := wsSvc.JoinWorkspace(ctx, partner.User.ID, inv.Code)
	if err != nil {
		t.Fatalf("JoinWorkspace: %v", err)
	}
	if joined.ID != owner.Workspace.ID || joined.Role != RoleMember {
		t.Fatalf("expected joined workspace %d as member, got id=%d role=%s", owner.Workspace.ID, joined.ID, joined.Role)
	}

	// Одноразовість: інший користувач не може використати той самий код
	_, err = wsSvc.JoinWorkspace(ctx, outsider.User.ID, inv.Code)
	assertAppErrorCode(t, err, http.StatusBadRequest, ErrInviteInvalidOrExpired)

	// Member не може створювати інвайт -> 403
	_, err = wsSvc.CreateInvite(ctx, partner.User.ID, owner.Workspace.ID)
	assertAppErrorCode(t, err, http.StatusForbidden, ErrForbidden)

	// Прострочений інвайт -> ERR_INVITE_INVALID_OR_EXPIRED
	if err := store.CreateInvite(ctx, owner.Workspace.ID, "expired-inv", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("seed expired invite: %v", err)
	}
	_, err = wsSvc.JoinWorkspace(ctx, outsider.User.ID, "expired-inv")
	assertAppErrorCode(t, err, http.StatusBadRequest, ErrInviteInvalidOrExpired)

	// Повторний join уже існуючого учасника через новий інвайт проходить без дублювання
	inv2, _ := wsSvc.CreateInvite(ctx, owner.User.ID, owner.Workspace.ID)
	if _, err := wsSvc.JoinWorkspace(ctx, partner.User.ID, inv2.Code); err != nil {
		t.Fatalf("re-join existing member failed: %v", err)
	}

	// Активація чужого воркспейсу -> 403
	err = wsSvc.ActivateWorkspace(ctx, outsider.User.ID, owner.Workspace.ID)
	assertAppErrorCode(t, err, http.StatusForbidden, ErrForbidden)
}

func TestWorkspaceSubscriptionAndAIQuotas(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir()) // TrialDailyAILimit=3, ActiveDailyAILimit=5
	store := NewPGStore(pool)
	auth, _ := NewAuthService(store, cfg, NewModuleRegistry(), nil)
	wsSvc := NewWorkspaceService(store, cfg, NewModuleRegistry(), nil)

	now := time.Date(2026, 11, 20, 22, 0, 0, 0, cfg.DefaultLocation)
	auth.now = func() time.Time { return now }
	wsSvc.now = func() time.Time { return now }

	ctx := context.Background()
	regRes, err := auth.Register(ctx, "quota@example.com", "StrongPass123", "Quota", "ФОП")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	wsID := regRes.Workspace.ID

	// Перевірка EnsureAIAllowed без ConsumeAIQuota НЕ збільшує лічильник
	for i := 0; i < 5; i++ {
		if err := wsSvc.EnsureAIAllowed(ctx, wsID); err != nil {
			t.Fatalf("EnsureAIAllowed #%d unexpected error: %v", i, err)
		}
	}

	// Конкурентне списання 3 запитів (рівно ліміт тріалу) не втрачає інкременти
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := wsSvc.ConsumeAIQuota(ctx, wsID); err != nil {
				t.Errorf("ConsumeAIQuota failed: %v", err)
			}
		}()
	}
	wg.Wait()

	state, _ := store.GetWorkspaceQuotaState(ctx, wsID)
	if state.AIRequestsToday != 3 {
		t.Fatalf("expected AIRequestsToday=3 after concurrent consume, got %d", state.AIRequestsToday)
	}

	// 4-й запит на тій самій добі -> 429 ERR_AI_QUOTA_EXCEEDED
	err = wsSvc.EnsureAIAllowed(ctx, wsID)
	assertAppErrorCode(t, err, http.StatusTooManyRequests, ErrAIQuotaExceeded)

	// Зміна дня в DEFAULT_TIMEZONE (після півночі за Києвом) скидає лічильник
	now = time.Date(2026, 11, 21, 0, 5, 0, 0, cfg.DefaultLocation)
	if err := wsSvc.EnsureAIAllowed(ctx, wsID); err != nil {
		t.Fatalf("expected quota reset on new day, got: %v", err)
	}

	// Перевіряємо гілку ai_requests_date < today в IncrementAIRequestsToday: після списання має бути 1, а не N+1
	if err := wsSvc.ConsumeAIQuota(ctx, wsID); err != nil {
		t.Fatalf("ConsumeAIQuota after day rollover failed: %v", err)
	}
	afterRollover, _ := store.GetWorkspaceQuotaState(ctx, wsID)
	if afterRollover.AIRequestsToday != 1 {
		t.Fatalf("expected AIRequestsToday=1 after day rollover consume, got %d", afterRollover.AIRequestsToday)
	}

	// Активна підписка має власний ліміт (ActiveDailyAILimit=5)
	if _, err := pool.Exec(ctx, `UPDATE workspaces SET plan_status = $1, ai_requests_today = 3, ai_requests_date = $2::date WHERE id = $3`,
		PlanStatusActive, now.Format("2006-01-02"), wsID); err != nil {
		t.Fatalf("update workspace to active: %v", err)
	}
	if err := wsSvc.EnsureAIAllowed(ctx, wsID); err != nil {
		t.Fatalf("expected active plan limit (5) to allow 4th request, got: %v", err)
	}

	// Прострочений subscription_ends_at -> 402 ERR_SUBSCRIPTION_EXPIRED
	now = regRes.Workspace.SubscriptionEndsAt.Add(time.Minute)
	err = wsSvc.EnsureAIAllowed(ctx, wsID)
	assertAppErrorCode(t, err, http.StatusPaymentRequired, ErrSubscriptionExpired)
}
