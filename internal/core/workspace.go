package core

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const inviteCodeNumBytes = 12

// WorkspaceInviteResult повертається при створенні інвайту для партнера.
type WorkspaceInviteResult struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WorkspaceService відповідає за воркспейси, інвайти партнерів, підписку та квоти AI.
type WorkspaceService struct {
	store    CoreStore
	cfg      *Config
	registry *ModuleRegistry
	logger   *Logger
	now      func() time.Time
}

// NewWorkspaceService створює сервіс управління воркспейсами.
func NewWorkspaceService(store CoreStore, cfg *Config, registry *ModuleRegistry, logger *Logger) *WorkspaceService {
	return &WorkspaceService{
		store:    store,
		cfg:      cfg,
		registry: registry,
		logger:   logger,
		now:      time.Now,
	}
}

// EffectivePlanStatus обчислює актуальний статус підписки з урахуванням subscription_ends_at.
func EffectivePlanStatus(ws Workspace, now time.Time) string {
	if !now.Before(ws.SubscriptionEndsAt) {
		return PlanStatusExpired
	}
	if ws.PlanStatus == PlanStatusActive || ws.PlanStatus == PlanStatusTrial {
		return ws.PlanStatus
	}
	return PlanStatusExpired
}

// CreateWorkspace створює додатковий воркспейс користувача, запускає сід модулів із компенсацією та робить його активним.
func (s *WorkspaceService) CreateWorkspace(ctx context.Context, userID int64, name, currency string) (Workspace, error) {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		return Workspace{}, NewAppError(http.StatusBadRequest, ErrValidation, "workspace name is required", nil)
	}
	if len([]rune(cleanName)) > maxNameLength {
		return Workspace{}, NewAppError(http.StatusBadRequest, ErrValidation, "workspace name exceeds maximum length", nil)
	}

	curr := strings.ToUpper(strings.TrimSpace(currency))
	if curr == "" {
		curr = s.cfg.DefaultCurrency
	} else if !isValidCurrencyCode(curr) {
		return Workspace{}, NewAppError(http.StatusBadRequest, ErrValidation, "invalid currency code", nil)
	}

	trialEndsAt := s.now().Add(time.Duration(s.cfg.TrialDays) * 24 * time.Hour)
	ws, err := s.store.CreateWorkspace(ctx, userID, cleanName, curr, trialEndsAt, s.cfg.MaxWorkspacesPerUser)
	if err != nil {
		return Workspace{}, err
	}

	if s.registry != nil {
		for _, mod := range s.registry.Modules() {
			if seedErr := mod.OnWorkspaceCreated(ctx, ws.ID); seedErr != nil {
				s.compensateDeleteWorkspace(ctx, userID, ws.ID, "workspace_seed_compensation_failed")
				return Workspace{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to initialize workspace modules", seedErr)
			}
		}
	}

	if err := s.store.SetActiveWorkspace(ctx, userID, ws.ID); err != nil {
		// Якщо активація впала після сіду, прибираємо новий воркспейс, щоб не витрачав ліміт користувача
		s.compensateDeleteWorkspace(ctx, userID, ws.ID, "workspace_activate_compensation_failed")
		return Workspace{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to activate workspace", err)
	}

	ws.PlanStatus = EffectivePlanStatus(ws, s.now())
	if s.logger != nil {
		s.logger.Slog().InfoContext(ctx, "workspace_created",
			slog.Int64("user_id", userID),
			slog.Int64("workspace_id", ws.ID),
		)
	}
	return ws, nil
}

func (s *WorkspaceService) compensateDeleteWorkspace(ctx context.Context, userID, workspaceID int64, logMsg string) {
	compCtx, cancel := context.WithTimeout(context.Background(), compensationTimeout)
	defer cancel()
	if compErr := s.store.DeleteWorkspace(compCtx, workspaceID); compErr != nil && s.logger != nil {
		s.logger.LogError(ctx, ErrInternal, logMsg,
			slog.Int64("user_id", userID),
			slog.Int64("workspace_id", workspaceID),
			slog.String("error", compErr.Error()),
		)
	}
}

// ActivateWorkspace змінює активний воркспейс користувача (лише якщо він є його учасником).
func (s *WorkspaceService) ActivateWorkspace(ctx context.Context, userID, workspaceID int64) error {
	if _, err := s.store.GetMembership(ctx, workspaceID, userID); err != nil {
		return err
	}
	return s.store.SetActiveWorkspace(ctx, userID, workspaceID)
}

// CheckMembership повертає роль користувача у воркспейсі або 403 ERR_FORBIDDEN.
func (s *WorkspaceService) CheckMembership(ctx context.Context, workspaceID, userID int64) (string, error) {
	return s.store.GetMembership(ctx, workspaceID, userID)
}

// CreateInvite генерує одноразовий інвайт-код для партнера (доступно лише власнику воркспейсу).
func (s *WorkspaceService) CreateInvite(ctx context.Context, userID, workspaceID int64) (WorkspaceInviteResult, error) {
	role, err := s.store.GetMembership(ctx, workspaceID, userID)
	if err != nil {
		return WorkspaceInviteResult{}, err
	}
	if role != RoleOwner {
		return WorkspaceInviteResult{}, NewAppError(http.StatusForbidden, ErrForbidden, "only workspace owner can create invites", nil)
	}

	code, err := generateSecureHex(inviteCodeNumBytes)
	if err != nil {
		return WorkspaceInviteResult{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to generate invite code", err)
	}
	expiresAt := s.now().Add(s.cfg.WorkspaceInviteTTL)
	if err := s.store.CreateInvite(ctx, workspaceID, code, expiresAt); err != nil {
		return WorkspaceInviteResult{}, err
	}

	return WorkspaceInviteResult{
		Code:      code,
		ExpiresAt: expiresAt,
	}, nil
}

// JoinWorkspace приєднує користувача до воркспейсу за одноразовим інвайт-кодом.
func (s *WorkspaceService) JoinWorkspace(ctx context.Context, userID int64, code string) (Workspace, error) {
	cleanCode := strings.TrimSpace(code)
	if cleanCode == "" {
		return Workspace{}, NewAppError(http.StatusBadRequest, ErrInviteInvalidOrExpired, "invite code is required", nil)
	}
	ws, err := s.store.JoinWorkspaceByInvite(ctx, userID, cleanCode)
	if err != nil {
		return Workspace{}, err
	}
	ws.PlanStatus = EffectivePlanStatus(ws, s.now())
	return ws, nil
}

// EnsureAIAllowed перевіряє активність підписки (402) та денну квоту AI (429) у DEFAULT_TIMEZONE перед викликом AI.
func (s *WorkspaceService) EnsureAIAllowed(ctx context.Context, workspaceID int64) error {
	ws, err := s.store.GetWorkspaceQuotaState(ctx, workspaceID)
	if err != nil {
		return err
	}

	now := s.now()
	status := EffectivePlanStatus(ws, now)
	if status == PlanStatusExpired {
		return NewAppError(http.StatusPaymentRequired, ErrSubscriptionExpired, "subscription or trial expired", nil)
	}

	dailyLimit := s.cfg.TrialDailyAILimit
	if status == PlanStatusActive {
		dailyLimit = s.cfg.ActiveDailyAILimit
	}

	todayLocal := now.In(s.cfg.DefaultLocation).Format("2006-01-02")
	storedDate := ws.AIRequestsDate.Format("2006-01-02")

	usedToday := ws.AIRequestsToday
	if storedDate < todayLocal {
		usedToday = 0
	}

	if usedToday >= dailyLimit {
		return NewAppError(http.StatusTooManyRequests, ErrAIQuotaExceeded, "daily AI quota exceeded", nil)
	}

	return nil
}

// ConsumeAIQuota списує 1 AI-запит за поточну добу в DEFAULT_TIMEZONE (викликається лише після успішної відповіді AI).
func (s *WorkspaceService) ConsumeAIQuota(ctx context.Context, workspaceID int64) error {
	todayInLoc := s.now().In(s.cfg.DefaultLocation)
	return s.store.IncrementAIRequestsToday(ctx, workspaceID, todayInLoc)
}
