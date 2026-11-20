package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLength      = 8
	maxPasswordBytes       = 72 // обмеження алгоритму bcrypt
	maxEmailLength         = 254
	maxNameLength          = 100
	tgLinkTokenNumBytes    = 16
	maxLoginAttemptEntries = 10000
	compensationTimeout    = 5 * time.Second
)

// AuthResult повертається при успішній реєстрації або вході.
type AuthResult struct {
	Token      string      `json:"token"`
	User       User        `json:"user"`
	Workspace  *Workspace  `json:"workspace,omitempty"`
	Workspaces []Workspace `json:"workspaces,omitempty"`
}

// MeResult повертається ендпоінтом GET /api/v1/auth/me.
type MeResult struct {
	User           User        `json:"user"`
	Workspaces     []Workspace `json:"workspaces"`
	TelegramLinked bool        `json:"telegram_linked"`
}

// TelegramLinkResult повертається ендпоінтом POST /api/v1/auth/telegram-link.
type TelegramLinkResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	DeepLink  string    `json:"deep_link,omitempty"`
}

type loginAttemptState struct {
	failures     int
	lastFailedAt time.Time
	lockedUntil  time.Time
}

// AuthService відповідає за реєстрацію, логін, JWT, захист від підбору пароля та токени прив'язки Telegram.
type AuthService struct {
	store     CoreStore
	cfg       *Config
	registry  *ModuleRegistry
	logger    *Logger
	now       func() time.Time
	dummyHash []byte

	mu       sync.Mutex
	attempts map[string]*loginAttemptState
}

// NewAuthService створює сервіс автентифікації.
func NewAuthService(store CoreStore, cfg *Config, registry *ModuleRegistry, logger *Logger) (*AuthService, error) {
	if store == nil || cfg == nil {
		return nil, fmt.Errorf("auth service: store and config are required")
	}
	// Попередньо рахуємо фіктивний хеш для вирівнювання часу відповіді при невідомому email
	dummy, err := bcrypt.GenerateFromPassword([]byte("kasabot-dummy-timing-check"), cfg.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("generate dummy bcrypt hash: %w", err)
	}
	return &AuthService{
		store:     store,
		cfg:       cfg,
		registry:  registry,
		logger:    logger,
		now:       time.Now,
		dummyHash: dummy,
		attempts:  make(map[string]*loginAttemptState),
	}, nil
}

// Register створює користувача, дефолтний воркспейс із тріалом і виконує сід зареєстрованих модулів.
func (s *AuthService) Register(ctx context.Context, email, password, name, workspaceName string) (AuthResult, error) {
	normEmail, err := normalizeAndValidateEmail(email)
	if err != nil {
		return AuthResult{}, err
	}
	if err := validatePassword(password); err != nil {
		return AuthResult{}, err
	}

	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		return AuthResult{}, NewAppError(http.StatusBadRequest, ErrValidation, "name is required", nil)
	}
	if len([]rune(cleanName)) > maxNameLength {
		return AuthResult{}, NewAppError(http.StatusBadRequest, ErrValidation, "name exceeds maximum length", nil)
	}

	cleanWSName := strings.TrimSpace(workspaceName)
	if cleanWSName == "" {
		cleanWSName = cleanName
	}
	if len([]rune(cleanWSName)) > maxNameLength {
		return AuthResult{}, NewAppError(http.StatusBadRequest, ErrValidation, "workspace_name exceeds maximum length", nil)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return AuthResult{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to hash password", err)
	}

	trialEndsAt := s.now().Add(time.Duration(s.cfg.TrialDays) * 24 * time.Hour)
	user, ws, err := s.store.CreateUserWithWorkspace(ctx, normEmail, string(hash), cleanName, cleanWSName, s.cfg.DefaultCurrency, trialEndsAt)
	if err != nil {
		return AuthResult{}, err
	}

	if s.registry != nil {
		for _, mod := range s.registry.Modules() {
			if seedErr := mod.OnWorkspaceCreated(ctx, ws.ID); seedErr != nil {
				// Компенсація: видаляємо воркспейс і користувача, щоб повторна реєстрація не дала ERR_EMAIL_TAKEN
				compCtx, cancel := context.WithTimeout(context.Background(), compensationTimeout)
				if compErr := s.store.DeleteUserAndWorkspace(compCtx, user.ID, ws.ID); compErr != nil && s.logger != nil {
					s.logger.LogError(ctx, ErrInternal, "register_compensation_failed",
						slog.Int64("user_id", user.ID),
						slog.Int64("workspace_id", ws.ID),
						slog.String("error", compErr.Error()),
					)
				}
				cancel()
				return AuthResult{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to initialize workspace modules", seedErr)
			}
		}
	}

	ws.PlanStatus = EffectivePlanStatus(ws, s.now())
	token, err := s.issueJWT(user.ID)
	if err != nil {
		return AuthResult{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to issue token", err)
	}

	if s.logger != nil {
		s.logger.Slog().InfoContext(ctx, "user_registered",
			slog.Int64("user_id", user.ID),
			slog.Int64("workspace_id", ws.ID),
		)
	}

	return AuthResult{
		Token:     token,
		User:      user,
		Workspace: &ws,
	}, nil
}

// Login перевіряє ліміт спроб, email і пароль та повертає JWT і список воркспейсів.
func (s *AuthService) Login(ctx context.Context, email, password, clientIP string) (AuthResult, error) {
	normEmail, err := normalizeAndValidateEmail(email)
	if err != nil {
		return AuthResult{}, NewAppError(http.StatusUnauthorized, ErrInvalidCredentials, "invalid credentials", nil)
	}

	attemptKey := normEmail + "|" + strings.TrimSpace(clientIP)
	if s.isLocked(attemptKey) {
		return AuthResult{}, NewAppError(http.StatusTooManyRequests, ErrTooManyAttempts, "too many login attempts", nil)
	}

	if password == "" || len(password) > maxPasswordBytes {
		s.recordFailure(attemptKey)
		return AuthResult{}, NewAppError(http.StatusUnauthorized, ErrInvalidCredentials, "invalid credentials", nil)
	}

	user, err := s.store.FindUserByEmail(ctx, normEmail)
	if err != nil {
		// Фіктивне порівняння для вирівнювання часу відповіді
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		s.recordFailure(attemptKey)
		return AuthResult{}, NewAppError(http.StatusUnauthorized, ErrInvalidCredentials, "invalid credentials", nil)
	}

	if cmpErr := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); cmpErr != nil {
		s.recordFailure(attemptKey)
		return AuthResult{}, NewAppError(http.StatusUnauthorized, ErrInvalidCredentials, "invalid credentials", nil)
	}

	s.resetFailures(attemptKey)

	workspaces, err := s.store.ListUserWorkspaces(ctx, user.ID)
	if err != nil {
		return AuthResult{}, err
	}
	now := s.now()
	for i := range workspaces {
		workspaces[i].PlanStatus = EffectivePlanStatus(workspaces[i], now)
	}

	token, err := s.issueJWT(user.ID)
	if err != nil {
		return AuthResult{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to issue token", err)
	}

	if s.logger != nil {
		s.logger.Slog().InfoContext(ctx, "user_logged_in", slog.Int64("user_id", user.ID))
	}

	return AuthResult{
		Token:      token,
		User:       user,
		Workspaces: workspaces,
	}, nil
}

// Me повертає профіль користувача, його воркспейси та статус прив'язки Telegram.
func (s *AuthService) Me(ctx context.Context, userID int64) (MeResult, error) {
	user, err := s.store.FindUserByID(ctx, userID)
	if err != nil {
		return MeResult{}, err
	}
	workspaces, err := s.store.ListUserWorkspaces(ctx, userID)
	if err != nil {
		return MeResult{}, err
	}
	now := s.now()
	for i := range workspaces {
		workspaces[i].PlanStatus = EffectivePlanStatus(workspaces[i], now)
	}
	return MeResult{
		User:           user,
		Workspaces:     workspaces,
		TelegramLinked: user.TelegramID != nil && *user.TelegramID > 0,
	}, nil
}

// GetActiveWorkspaceID повертає ID активного воркспейсу користувача або ERR_VALIDATION, якщо його не задано.
func (s *AuthService) GetActiveWorkspaceID(ctx context.Context, userID int64) (int64, error) {
	user, err := s.store.FindUserByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	if user.ActiveWorkspaceID == nil || *user.ActiveWorkspaceID <= 0 {
		return 0, NewAppError(http.StatusBadRequest, ErrValidation, "active workspace is not set", nil)
	}
	return *user.ActiveWorkspaceID, nil
}

// CreateTelegramLink генерує одноразовий токен прив'язки Telegram з TTL із конфігу.
func (s *AuthService) CreateTelegramLink(ctx context.Context, userID int64) (TelegramLinkResult, error) {
	token, err := generateSecureHex(tgLinkTokenNumBytes)
	if err != nil {
		return TelegramLinkResult{}, NewAppError(http.StatusInternalServerError, ErrInternal, "failed to generate telegram link token", err)
	}
	expiresAt := s.now().Add(s.cfg.TGLinkTokenTTL)
	if err := s.store.CreateTelegramLinkToken(ctx, userID, token, expiresAt); err != nil {
		return TelegramLinkResult{}, err
	}

	var deepLink string
	if s.cfg.TGBotUsername != "" {
		deepLink = fmt.Sprintf("https://t.me/%s?start=link_%s", s.cfg.TGBotUsername, token)
	}
	return TelegramLinkResult{
		Token:     token,
		ExpiresAt: expiresAt,
		DeepLink:  deepLink,
	}, nil
}

// ConsumeTelegramLinkToken прив'язує telegram_id до користувача за одноразовим токеном (для Telegram-бота в Плані #004).
func (s *AuthService) ConsumeTelegramLinkToken(ctx context.Context, token string, telegramID int64) (int64, error) {
	cleanToken := strings.TrimSpace(token)
	if cleanToken == "" || telegramID <= 0 {
		return 0, NewAppError(http.StatusBadRequest, ErrTelegramLinkExpired, "telegram link token is invalid or expired", nil)
	}
	return s.store.ConsumeTelegramLinkToken(ctx, cleanToken, telegramID)
}

// ParseToken перевіряє підпис HS256, строк дії JWT та повертає user_id.
func (s *AuthService) ParseToken(tokenString string) (int64, error) {
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(t *jwt.Token) (any, error) {
			return []byte(s.cfg.JWTSecret), nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !parsed.Valid || claims.Subject == "" {
		return 0, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "invalid or expired token", nil)
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		return 0, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "invalid token subject", nil)
	}
	return userID, nil
}

func (s *AuthService) issueJWT(userID int64) (string, error) {
	now := s.now()
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.JWTTTL)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

func (s *AuthService) isLocked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.attempts[key]
	if !ok {
		return false
	}
	now := s.now()
	if !state.lockedUntil.IsZero() {
		if now.Before(state.lockedUntil) {
			return true
		}
		delete(s.attempts, key)
		return false
	}
	if !state.lastFailedAt.IsZero() && !now.Before(state.lastFailedAt.Add(s.cfg.LoginLockDuration)) {
		delete(s.attempts, key)
	}
	return false
}

func (s *AuthService) recordFailure(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	s.cleanupStaleAttemptsLocked(now)

	state, ok := s.attempts[key]
	if !ok {
		if len(s.attempts) >= maxLoginAttemptEntries {
			s.evictOldestAttemptLocked()
		}
		state = &loginAttemptState{}
		s.attempts[key] = state
	} else if !state.lastFailedAt.IsZero() && !now.Before(state.lastFailedAt.Add(s.cfg.LoginLockDuration)) {
		state.failures = 0
		state.lockedUntil = time.Time{}
	}

	state.failures++
	state.lastFailedAt = now
	if state.failures >= s.cfg.LoginMaxAttempts {
		state.lockedUntil = now.Add(s.cfg.LoginLockDuration)
	}
}

func (s *AuthService) cleanupStaleAttemptsLocked(now time.Time) {
	for k, st := range s.attempts {
		if !st.lockedUntil.IsZero() {
			if !now.Before(st.lockedUntil) {
				delete(s.attempts, k)
			}
			continue
		}
		if !st.lastFailedAt.IsZero() && !now.Before(st.lastFailedAt.Add(s.cfg.LoginLockDuration)) {
			delete(s.attempts, k)
		}
	}
}

func (s *AuthService) evictOldestAttemptLocked() {
	var oldestUnlockedKey, oldestLockedKey string
	var oldestUnlockedTime, oldestLockedTime time.Time

	// Спочатку шукаємо найстаріший незаблокований запис, щоб флуд не знімав активні блокування
	for k, st := range s.attempts {
		if st.lockedUntil.IsZero() {
			if oldestUnlockedKey == "" || st.lastFailedAt.Before(oldestUnlockedTime) {
				oldestUnlockedKey = k
				oldestUnlockedTime = st.lastFailedAt
			}
		} else if oldestLockedKey == "" || st.lastFailedAt.Before(oldestLockedTime) {
			oldestLockedKey = k
			oldestLockedTime = st.lastFailedAt
		}
	}

	if oldestUnlockedKey != "" {
		delete(s.attempts, oldestUnlockedKey)
	} else if oldestLockedKey != "" {
		delete(s.attempts, oldestLockedKey)
	}
}

func (s *AuthService) resetFailures(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, key)
}

func normalizeAndValidateEmail(raw string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(raw))
	if norm == "" {
		return "", NewAppError(http.StatusBadRequest, ErrValidation, "email is required", nil)
	}
	if len(norm) > maxEmailLength {
		return "", NewAppError(http.StatusBadRequest, ErrValidation, "email exceeds maximum length", nil)
	}
	addr, err := mail.ParseAddress(norm)
	if err != nil || addr.Address != norm || !strings.Contains(norm, ".") {
		return "", NewAppError(http.StatusBadRequest, ErrValidation, "invalid email format", nil)
	}
	return norm, nil
}

func validatePassword(password string) error {
	if len([]rune(password)) < minPasswordLength {
		return NewAppError(http.StatusBadRequest, ErrValidation, "password must be at least 8 characters", nil)
	}
	if len(password) > maxPasswordBytes {
		return NewAppError(http.StatusBadRequest, ErrValidation, "password exceeds maximum length of 72 bytes", nil)
	}
	return nil
}

func generateSecureHex(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
