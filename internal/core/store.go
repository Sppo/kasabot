package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ролі учасників воркспейсу та статуси підписки (доменні константи).
const (
	RoleOwner  = "owner"
	RoleMember = "member"

	PlanStatusTrial   = "trial"
	PlanStatusActive  = "active"
	PlanStatusExpired = "expired"

	pgUniqueViolationCode = "23505"
)

// User представляє запис користувача в таблиці users.
type User struct {
	ID                int64     `json:"id"`
	Email             string    `json:"email"`
	PasswordHash      string    `json:"-"`
	Name              string    `json:"name"`
	TelegramID        *int64    `json:"telegram_id,omitempty"`
	ActiveWorkspaceID *int64    `json:"active_workspace_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Workspace представляє простір обліку в таблиці workspaces.
type Workspace struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	Currency           string    `json:"currency"`
	OwnerID            int64     `json:"owner_id"`
	PlanStatus         string    `json:"plan_status"`
	SubscriptionEndsAt time.Time `json:"subscription_ends_at"`
	AIRequestsToday    int       `json:"ai_requests_today"`
	AIRequestsDate     time.Time `json:"ai_requests_date"`
	Role               string    `json:"role,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// CoreStore ізолює всі SQL-запити до системних таблиць ядра від бізнес-логіки.
type CoreStore interface {
	CreateUserWithWorkspace(ctx context.Context, email, passwordHash, name, wsName, currency string, trialEndsAt time.Time) (User, Workspace, error)
	DeleteUserAndWorkspace(ctx context.Context, userID, workspaceID int64) error
	FindUserByEmail(ctx context.Context, email string) (User, error)
	FindUserByID(ctx context.Context, userID int64) (User, error)

	CreateWorkspace(ctx context.Context, ownerID int64, name, currency string, trialEndsAt time.Time, maxWorkspaces int) (Workspace, error)
	SetActiveWorkspace(ctx context.Context, userID, workspaceID int64) error
	DeleteWorkspace(ctx context.Context, workspaceID int64) error
	ListUserWorkspaces(ctx context.Context, userID int64) ([]Workspace, error)
	GetMembership(ctx context.Context, workspaceID, userID int64) (string, error)

	CreateInvite(ctx context.Context, workspaceID int64, code string, expiresAt time.Time) error
	JoinWorkspaceByInvite(ctx context.Context, userID int64, code string) (Workspace, error)

	CreateTelegramLinkToken(ctx context.Context, userID int64, token string, expiresAt time.Time) error
	ConsumeTelegramLinkToken(ctx context.Context, token string, telegramID int64) (int64, error)

	GetWorkspaceQuotaState(ctx context.Context, workspaceID int64) (Workspace, error)
	IncrementAIRequestsToday(ctx context.Context, workspaceID int64, today time.Time) error
}

// PGStore реалізує CoreStore поверх pgxpool.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore створює PostgreSQL-сховище ядра.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationCode
}

func (s *PGStore) CreateUserWithWorkspace(ctx context.Context, email, passwordHash, name, wsName, currency string, trialEndsAt time.Time) (User, Workspace, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, Workspace{}, fmt.Errorf("begin register tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var u User
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name)
		 VALUES ($1, $2, $3)
		 RETURNING id, email, password_hash, name, telegram_id, active_workspace_id, created_at`,
		email, passwordHash, name,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.TelegramID, &u.ActiveWorkspaceID, &u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, Workspace{}, NewAppError(http.StatusConflict, ErrEmailTaken, "email already registered", nil)
		}
		return User{}, Workspace{}, fmt.Errorf("insert user: %w", err)
	}

	var ws Workspace
	err = tx.QueryRow(ctx,
		`INSERT INTO workspaces (name, currency, owner_id, plan_status, subscription_ends_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, currency, owner_id, plan_status, subscription_ends_at, ai_requests_today, ai_requests_date, created_at`,
		wsName, currency, u.ID, PlanStatusTrial, trialEndsAt,
	).Scan(&ws.ID, &ws.Name, &ws.Currency, &ws.OwnerID, &ws.PlanStatus, &ws.SubscriptionEndsAt, &ws.AIRequestsToday, &ws.AIRequestsDate, &ws.CreatedAt)
	if err != nil {
		return User{}, Workspace{}, fmt.Errorf("insert default workspace: %w", err)
	}
	ws.Role = RoleOwner

	if _, err := tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`,
		ws.ID, u.ID, RoleOwner,
	); err != nil {
		return User{}, Workspace{}, fmt.Errorf("insert workspace owner: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET active_workspace_id = $1 WHERE id = $2`,
		ws.ID, u.ID,
	); err != nil {
		return User{}, Workspace{}, fmt.Errorf("set active workspace: %w", err)
	}
	u.ActiveWorkspaceID = &ws.ID

	if err := tx.Commit(ctx); err != nil {
		return User{}, Workspace{}, fmt.Errorf("commit register tx: %w", err)
	}
	return u, ws, nil
}

func (s *PGStore) DeleteUserAndWorkspace(ctx context.Context, userID, workspaceID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin compensation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Спочатку видаляємо воркспейс, бо workspaces.owner_id посилається на users(id) без CASCADE
	if _, err := tx.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete workspace %d: %w", workspaceID, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("delete user %d: %w", userID, err)
	}
	return tx.Commit(ctx)
}

func (s *PGStore) FindUserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, name, telegram_id, active_workspace_id, created_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.TelegramID, &u.ActiveWorkspaceID, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, NewAppError(http.StatusNotFound, ErrNotFound, "user not found", nil)
		}
		return User{}, fmt.Errorf("find user by email: %w", err)
	}
	return u, nil
}

func (s *PGStore) FindUserByID(ctx context.Context, userID int64) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, name, telegram_id, active_workspace_id, created_at
		 FROM users WHERE id = $1`,
		userID,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.TelegramID, &u.ActiveWorkspaceID, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, NewAppError(http.StatusNotFound, ErrNotFound, "user not found", nil)
		}
		return User{}, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

func (s *PGStore) CreateWorkspace(ctx context.Context, ownerID int64, name, currency string, trialEndsAt time.Time, maxWorkspaces int) (Workspace, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, fmt.Errorf("begin create workspace tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Блокуємо рядок користувача, щоб конкурентне створення не обійшло ліміт MAX_WORKSPACES_PER_USER
	var lockedID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, ownerID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Workspace{}, NewAppError(http.StatusNotFound, ErrNotFound, "user not found", nil)
		}
		return Workspace{}, fmt.Errorf("lock user for workspace limit: %w", err)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM workspaces WHERE owner_id = $1`, ownerID).Scan(&count); err != nil {
		return Workspace{}, fmt.Errorf("count owned workspaces: %w", err)
	}
	if count >= maxWorkspaces {
		return Workspace{}, NewAppError(http.StatusForbidden, ErrForbidden, "workspace limit reached", nil)
	}

	var ws Workspace
	err = tx.QueryRow(ctx,
		`INSERT INTO workspaces (name, currency, owner_id, plan_status, subscription_ends_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, currency, owner_id, plan_status, subscription_ends_at, ai_requests_today, ai_requests_date, created_at`,
		name, currency, ownerID, PlanStatusTrial, trialEndsAt,
	).Scan(&ws.ID, &ws.Name, &ws.Currency, &ws.OwnerID, &ws.PlanStatus, &ws.SubscriptionEndsAt, &ws.AIRequestsToday, &ws.AIRequestsDate, &ws.CreatedAt)
	if err != nil {
		return Workspace{}, fmt.Errorf("insert workspace: %w", err)
	}
	ws.Role = RoleOwner

	if _, err := tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`,
		ws.ID, ownerID, RoleOwner,
	); err != nil {
		return Workspace{}, fmt.Errorf("insert workspace owner member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, fmt.Errorf("commit create workspace tx: %w", err)
	}
	return ws, nil
}

func (s *PGStore) SetActiveWorkspace(ctx context.Context, userID, workspaceID int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET active_workspace_id = $1 WHERE id = $2`,
		workspaceID, userID,
	)
	if err != nil {
		return fmt.Errorf("set active workspace: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return NewAppError(http.StatusNotFound, ErrNotFound, "user not found", nil)
	}
	return nil
}

func (s *PGStore) DeleteWorkspace(ctx context.Context, workspaceID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete workspace %d: %w", workspaceID, err)
	}
	return nil
}

func (s *PGStore) ListUserWorkspaces(ctx context.Context, userID int64) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT w.id, w.name, w.currency, w.owner_id, w.plan_status, w.subscription_ends_at,
		        w.ai_requests_today, w.ai_requests_date, wm.role, w.created_at
		 FROM workspaces w
		 JOIN workspace_members wm ON wm.workspace_id = w.id
		 WHERE wm.user_id = $1
		 ORDER BY w.id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list user workspaces: %w", err)
	}
	defer rows.Close()

	var result []Workspace
	for rows.Next() {
		var ws Workspace
		if err := rows.Scan(
			&ws.ID, &ws.Name, &ws.Currency, &ws.OwnerID, &ws.PlanStatus, &ws.SubscriptionEndsAt,
			&ws.AIRequestsToday, &ws.AIRequestsDate, &ws.Role, &ws.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		result = append(result, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return result, nil
}

func (s *PGStore) GetMembership(ctx context.Context, workspaceID, userID int64) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", NewAppError(http.StatusForbidden, ErrForbidden, "access to workspace denied", nil)
		}
		return "", fmt.Errorf("get membership: %w", err)
	}
	return role, nil
}

func (s *PGStore) CreateInvite(ctx context.Context, workspaceID int64, code string, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO workspace_invites (code, workspace_id, expires_at) VALUES ($1, $2, $3)`,
		code, workspaceID, expiresAt,
	); err != nil {
		return fmt.Errorf("insert workspace invite: %w", err)
	}
	return nil
}

func (s *PGStore) JoinWorkspaceByInvite(ctx context.Context, userID int64, code string) (Workspace, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, fmt.Errorf("begin join invite tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var workspaceID int64
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT workspace_id, expires_at FROM workspace_invites WHERE code = $1 FOR UPDATE`,
		code,
	).Scan(&workspaceID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Workspace{}, NewAppError(http.StatusBadRequest, ErrInviteInvalidOrExpired, "invite code is invalid or expired", nil)
		}
		return Workspace{}, fmt.Errorf("find workspace invite: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM workspace_invites WHERE code = $1`, code); err != nil {
		return Workspace{}, fmt.Errorf("delete used invite: %w", err)
	}

	if !expiresAt.After(time.Now()) {
		// Комітимо видалення простроченого інвайту і повертаємо помилку
		_ = tx.Commit(ctx)
		return Workspace{}, NewAppError(http.StatusBadRequest, ErrInviteInvalidOrExpired, "invite code is invalid or expired", nil)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		workspaceID, userID, RoleMember,
	); err != nil {
		return Workspace{}, fmt.Errorf("insert workspace member: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET active_workspace_id = $1 WHERE id = $2`,
		workspaceID, userID,
	); err != nil {
		return Workspace{}, fmt.Errorf("activate joined workspace: %w", err)
	}

	var ws Workspace
	err = tx.QueryRow(ctx,
		`SELECT w.id, w.name, w.currency, w.owner_id, w.plan_status, w.subscription_ends_at,
		        w.ai_requests_today, w.ai_requests_date, wm.role, w.created_at
		 FROM workspaces w
		 JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = $2
		 WHERE w.id = $1`,
		workspaceID, userID,
	).Scan(&ws.ID, &ws.Name, &ws.Currency, &ws.OwnerID, &ws.PlanStatus, &ws.SubscriptionEndsAt, &ws.AIRequestsToday, &ws.AIRequestsDate, &ws.Role, &ws.CreatedAt)
	if err != nil {
		return Workspace{}, fmt.Errorf("load joined workspace: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, fmt.Errorf("commit join invite tx: %w", err)
	}
	return ws, nil
}

func (s *PGStore) CreateTelegramLinkToken(ctx context.Context, userID int64, token string, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO telegram_link_tokens (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expiresAt,
	); err != nil {
		return fmt.Errorf("insert telegram link token: %w", err)
	}
	return nil
}

func (s *PGStore) ConsumeTelegramLinkToken(ctx context.Context, token string, telegramID int64) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin consume telegram token tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID int64
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT user_id, expires_at FROM telegram_link_tokens WHERE token = $1 FOR UPDATE`,
		token,
	).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, NewAppError(http.StatusBadRequest, ErrTelegramLinkExpired, "telegram link token is invalid or expired", nil)
		}
		return 0, fmt.Errorf("find telegram link token: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM telegram_link_tokens WHERE token = $1`, token); err != nil {
		return 0, fmt.Errorf("delete telegram link token: %w", err)
	}

	if !expiresAt.After(time.Now()) {
		_ = tx.Commit(ctx)
		return 0, NewAppError(http.StatusBadRequest, ErrTelegramLinkExpired, "telegram link token is invalid or expired", nil)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET telegram_id = $1 WHERE id = $2`,
		telegramID, userID,
	); err != nil {
		if isUniqueViolation(err) {
			return 0, NewAppError(http.StatusConflict, ErrValidation, "telegram account is already linked to another user", nil)
		}
		return 0, fmt.Errorf("link telegram_id: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit consume telegram token tx: %w", err)
	}
	return userID, nil
}

func (s *PGStore) GetWorkspaceQuotaState(ctx context.Context, workspaceID int64) (Workspace, error) {
	var ws Workspace
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, currency, owner_id, plan_status, subscription_ends_at, ai_requests_today, ai_requests_date, created_at
		 FROM workspaces WHERE id = $1`,
		workspaceID,
	).Scan(&ws.ID, &ws.Name, &ws.Currency, &ws.OwnerID, &ws.PlanStatus, &ws.SubscriptionEndsAt, &ws.AIRequestsToday, &ws.AIRequestsDate, &ws.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Workspace{}, NewAppError(http.StatusNotFound, ErrNotFound, "workspace not found", nil)
		}
		return Workspace{}, fmt.Errorf("get workspace quota state: %w", err)
	}
	return ws, nil
}

func (s *PGStore) IncrementAIRequestsToday(ctx context.Context, workspaceID int64, today time.Time) error {
	dateStr := today.Format("2006-01-02")
	tag, err := s.pool.Exec(ctx,
		`UPDATE workspaces
		 SET ai_requests_today = CASE WHEN ai_requests_date < $2::date THEN 1 ELSE ai_requests_today + 1 END,
		     ai_requests_date = $2::date
		 WHERE id = $1`,
		workspaceID, dateStr,
	)
	if err != nil {
		return fmt.Errorf("increment ai_requests_today: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return NewAppError(http.StatusNotFound, ErrNotFound, "workspace not found", nil)
	}
	return nil
}
