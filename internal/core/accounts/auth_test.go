package accounts

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"gopkg.in/telebot.v3"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/config"
	"kasabot/internal/core/logging"
	"kasabot/internal/core/module"
	"kasabot/internal/core/testkit"
)

func newAuthTestConfig(t *testing.T, logDir string) *config.Config {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Kyiv")
	return &config.Config{
		Port:                 8080,
		DBMaxConns:           5,
		DBConnectTimeout:     5 * time.Second,
		HTTPTimeout:          10 * time.Second,
		MaxJSONBodyBytes:     1024 * 1024,
		JWTSecret:            "test-jwt-secret-key-at-least-16-chars",
		JWTTTL:               24 * time.Hour,
		TrialDays:            14,
		TrialDailyAILimit:    3,
		ActiveDailyAILimit:   5,
		WorkspaceInviteTTL:   48 * time.Hour,
		TGLinkTokenTTL:       10 * time.Minute,
		DefaultTimezone:      "Europe/Kyiv",
		DefaultLocation:      loc,
		DefaultCurrency:      "UAH",
		LogDir:               logDir,
		LogRetentionDays:     0,
		BcryptCost:           10, // мінімально дозволений у конфігу, щоб тести йшли швидко
		LoginMaxAttempts:     3,
		LoginLockDuration:    15 * time.Minute,
		MaxWorkspacesPerUser: 2,
		TGBotUsername:        "KasaTestBot",
	}
}

type seedFailModule struct {
	name string
	fail bool
}

func (m *seedFailModule) Name() string                   { return m.name }
func (m *seedFailModule) Migrations() []module.Migration { return nil }
func (m *seedFailModule) RegisterRoutes(_ chi.Router)    {}
func (m *seedFailModule) RegisterBot(_ *telebot.Bot)     {}
func (m *seedFailModule) OnWorkspaceCreated(_ context.Context, _ int64) error {
	if m.fail {
		return errors.New("seed boom")
	}
	return nil
}

func assertAppErrorCode(t *testing.T, err error, wantStatus int, wantCode apperr.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s (%d), got nil", wantCode, wantStatus)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperr.AppError, got %T: %v", err, err)
	}
	if appErr.Status != wantStatus || appErr.Code != wantCode {
		t.Fatalf("expected status=%d code=%s, got status=%d code=%s (%v)", wantStatus, wantCode, appErr.Status, appErr.Code, err)
	}
}

func TestRegisterAndDuplicateEmail(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	store := NewPGStore(pool)
	auth, err := NewAuthService(store, cfg, module.NewModuleRegistry(), nil)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	ctx := context.Background()
	res, err := auth.Register(ctx, "User@Example.COM ", "StrongPass123", "Olena", "Мій ФОП")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if res.Token == "" || res.Workspace == nil {
		t.Fatal("expected JWT token and default workspace")
	}
	if res.User.Email != "user@example.com" {
		t.Errorf("expected normalized lowercase email, got %q", res.User.Email)
	}
	if res.Workspace.PlanStatus != PlanStatusTrial || res.Workspace.Role != RoleOwner {
		t.Errorf("expected trial owner workspace, got status=%s role=%s", res.Workspace.PlanStatus, res.Workspace.Role)
	}
	if res.User.ActiveWorkspaceID == nil || *res.User.ActiveWorkspaceID != res.Workspace.ID {
		t.Errorf("expected active_workspace_id=%d, got %v", res.Workspace.ID, res.User.ActiveWorkspaceID)
	}

	// Дубль email у іншому регістрі -> ERR_EMAIL_TAKEN
	_, err = auth.Register(ctx, "user@example.com", "StrongPass123", "Olena2", "")
	assertAppErrorCode(t, err, http.StatusConflict, apperr.ErrEmailTaken)

	// Слабкий пароль (< 8) -> ERR_VALIDATION
	_, err = auth.Register(ctx, "other@example.com", "short", "Olena", "")
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrValidation)

	// Задовгий пароль (> 72 байт) -> ERR_VALIDATION
	_, err = auth.Register(ctx, "other2@example.com", strings.Repeat("a", 73), "Olena", "")
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrValidation)

	// Довжина полів: email > 254, name > 100, workspace_name > 100 -> ERR_VALIDATION
	_, err = auth.Register(ctx, strings.Repeat("a", 246)+"@test.com", "StrongPass123", "Olena", "")
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrValidation)

	_, err = auth.Register(ctx, "valid1@example.com", "StrongPass123", strings.Repeat("а", 101), "")
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrValidation)

	_, err = auth.Register(ctx, "valid2@example.com", "StrongPass123", "Olena", strings.Repeat("б", 101))
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrValidation)
}

func TestLoginSecurityAndBruteForceLock(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	store := NewPGStore(pool)
	auth, err := NewAuthService(store, cfg, module.NewModuleRegistry(), nil)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	ctx := context.Background()
	if _, err := auth.Register(ctx, "ivan@example.com", "CorrectPass123", "Ivan", "ФОП"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Невідомий email та хибний пароль дають однакову відповідь
	_, errUnknown := auth.Login(ctx, "nobody@example.com", "WrongPass123", "1.2.3.4")
	_, errWrong := auth.Login(ctx, "ivan@example.com", "WrongPass123", "1.2.3.4")
	assertAppErrorCode(t, errUnknown, http.StatusUnauthorized, apperr.ErrInvalidCredentials)
	assertAppErrorCode(t, errWrong, http.StatusUnauthorized, apperr.ErrInvalidCredentials)

	// Добиваємо до LOGIN_MAX_ATTEMPTS = 3 помилок для ivan@example.com
	_, _ = auth.Login(ctx, "ivan@example.com", "WrongPass123", "1.2.3.4")
	_, _ = auth.Login(ctx, "ivan@example.com", "WrongPass123", "1.2.3.4")

	// Наступна спроба (навіть із ПРАВИЛЬНИМ паролем) -> 429 ERR_TOO_MANY_ATTEMPTS
	_, errLocked := auth.Login(ctx, "ivan@example.com", "CorrectPass123", "1.2.3.4")
	assertAppErrorCode(t, errLocked, http.StatusTooManyRequests, apperr.ErrTooManyAttempts)

	// Інша IP не блокується разом із першою (ключ email|IP)
	resOtherIP, errOtherIP := auth.Login(ctx, "ivan@example.com", "CorrectPass123", "5.6.7.8")
	if errOtherIP != nil || resOtherIP.Token == "" {
		t.Fatalf("expected login from different IP to succeed, got: %v", errOtherIP)
	}

	// Після завершення блокування вхід із правильним паролем проходить
	now = now.Add(cfg.LoginLockDuration + time.Second)
	res, err := auth.Login(ctx, "ivan@example.com", "CorrectPass123", "1.2.3.4")
	if err != nil {
		t.Fatalf("expected login after lock expiry to succeed, got: %v", err)
	}
	if res.Token == "" {
		t.Fatal("expected JWT token after unlock")
	}

	// Згасання спроб, які не дійшли до ліміту (2 помилки < 3, потім пауза >= LOGIN_LOCK_MINUTES, потім ще 1 помилка -> не блокує)
	_, _ = auth.Login(ctx, "ivan@example.com", "WrongPass123", "9.9.9.9")
	_, _ = auth.Login(ctx, "ivan@example.com", "WrongPass123", "9.9.9.9")
	now = now.Add(cfg.LoginLockDuration + time.Second)
	_, _ = auth.Login(ctx, "ivan@example.com", "WrongPass123", "9.9.9.9")
	if _, err := auth.Login(ctx, "ivan@example.com", "CorrectPass123", "9.9.9.9"); err != nil {
		t.Fatalf("expected decayed attempts not to lock account, got: %v", err)
	}

	// Застарілі записи з інших ключів видаляються при наступних невдалих спробах
	_, _ = auth.Login(ctx, "bot1@example.com", "WrongPass123", "10.0.0.1")
	now = now.Add(cfg.LoginLockDuration + time.Second)
	_, _ = auth.Login(ctx, "bot2@example.com", "WrongPass123", "10.0.0.2")
	auth.mu.Lock()
	_, bot1Exists := auth.attempts["bot1@example.com|10.0.0.1"]
	auth.mu.Unlock()
	if bot1Exists {
		t.Fatal("expected stale attempt entry bot1 to be evicted")
	}

	// Витіснення за лімітом мапи: активне блокування НЕ витісняється, доки є незаблоковані записи
	auth.mu.Lock()
	auth.attempts["locked@example.com|1.1.1.1"] = &loginAttemptState{
		failures:     3,
		lastFailedAt: now.Add(-2 * time.Minute),
		lockedUntil:  now.Add(10 * time.Minute),
	}
	auth.attempts["unlocked@example.com|2.2.2.2"] = &loginAttemptState{
		failures:     1,
		lastFailedAt: now.Add(-time.Minute),
	}
	auth.evictOldestAttemptLocked()
	_, lockedStillThere := auth.attempts["locked@example.com|1.1.1.1"]
	_, unlockedStillThere := auth.attempts["unlocked@example.com|2.2.2.2"]
	auth.mu.Unlock()
	if !lockedStillThere || unlockedStillThere {
		t.Fatalf("expected unlocked entry to be evicted and locked entry preserved (locked=%v, unlocked=%v)", lockedStillThere, unlockedStillThere)
	}
}

func TestJWTValidation(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	auth, err := NewAuthService(NewPGStore(pool), cfg, module.NewModuleRegistry(), nil)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	token, err := auth.issueJWT(42)
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	uid, err := auth.ParseToken(token)
	if err != nil || uid != 42 {
		t.Fatalf("ParseToken valid: got uid=%d err=%v", uid, err)
	}

	// Прострочений токен -> 401
	now = now.Add(cfg.JWTTTL + time.Minute)
	_, err = auth.ParseToken(token)
	assertAppErrorCode(t, err, http.StatusUnauthorized, apperr.ErrUnauthorized)
	now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// Чужий секрет -> 401
	foreignTok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "42",
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte("another-secret-key-at-least-16"))
	_, err = auth.ParseToken(foreignTok)
	assertAppErrorCode(t, err, http.StatusUnauthorized, apperr.ErrUnauthorized)

	// alg=none -> 401
	noneTok, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject:   "42",
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err = auth.ParseToken(noneTok)
	assertAppErrorCode(t, err, http.StatusUnauthorized, apperr.ErrUnauthorized)

	// Інший алгоритм HS384 -> 401
	hs384Tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS384, jwt.RegisteredClaims{
		Subject:   "42",
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte(cfg.JWTSecret))
	_, err = auth.ParseToken(hs384Tok)
	assertAppErrorCode(t, err, http.StatusUnauthorized, apperr.ErrUnauthorized)

	// Токен без exp -> 401
	noExpTok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: "42",
	}).SignedString([]byte(cfg.JWTSecret))
	_, err = auth.ParseToken(noExpTok)
	assertAppErrorCode(t, err, http.StatusUnauthorized, apperr.ErrUnauthorized)
}

func TestTelegramLinkTokenFlow(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())
	auth, err := NewAuthService(NewPGStore(pool), cfg, module.NewModuleRegistry(), nil)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	ctx := context.Background()
	u1, err := auth.Register(ctx, "u1@example.com", "StrongPass123", "U1", "WS1")
	if err != nil {
		t.Fatalf("register u1: %v", err)
	}
	u2, err := auth.Register(ctx, "u2@example.com", "StrongPass123", "U2", "WS2")
	if err != nil {
		t.Fatalf("register u2: %v", err)
	}

	link1, err := auth.CreateTelegramLink(ctx, u1.User.ID)
	if err != nil {
		t.Fatalf("CreateTelegramLink: %v", err)
	}
	if !strings.Contains(link1.DeepLink, "https://t.me/KasaTestBot?start=link_"+link1.Token) {
		t.Errorf("unexpected deep link: %q", link1.DeepLink)
	}

	// Успішна прив'язка
	linkedUID, err := auth.ConsumeTelegramLinkToken(ctx, link1.Token, 999001)
	if err != nil || linkedUID != u1.User.ID {
		t.Fatalf("ConsumeTelegramLinkToken failed: uid=%d err=%v", linkedUID, err)
	}

	// Одноразовість: повторне використання -> ERR_TELEGRAM_LINK_EXPIRED
	_, err = auth.ConsumeTelegramLinkToken(ctx, link1.Token, 999001)
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrTelegramLinkExpired)

	// Прострочений токен -> ERR_TELEGRAM_LINK_EXPIRED
	if err := auth.store.CreateTelegramLinkToken(ctx, u2.User.ID, "expired-tok", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}
	_, err = auth.ConsumeTelegramLinkToken(ctx, "expired-tok", 999002)
	assertAppErrorCode(t, err, http.StatusBadRequest, apperr.ErrTelegramLinkExpired)

	// Повторна прив'язка того ж telegram_id іншому користувачу відхиляється
	link2, _ := auth.CreateTelegramLink(ctx, u2.User.ID)
	_, err = auth.ConsumeTelegramLinkToken(ctx, link2.Token, 999001)
	assertAppErrorCode(t, err, http.StatusConflict, apperr.ErrValidation)
}

func TestRegisterModuleSeedCompensation(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())

	var stdoutBuf bytes.Buffer
	logger, err := logging.NewLogger(cfg, logging.WithLoggerStdout(&stdoutBuf))
	if err != nil {
		t.Fatalf("logging.NewLogger: %v", err)
	}
	defer logger.Close()

	mod := &seedFailModule{name: "finance", fail: true}
	reg := module.NewModuleRegistry()
	_ = reg.Register(mod)

	store := NewPGStore(pool)
	auth, err := NewAuthService(store, cfg, reg, logger)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}
	wsSvc := NewWorkspaceService(store, cfg, reg, logger)
	handler := NewAuthHandler(auth, wsSvc, cfg, logger).Routes()

	// Спроба реєстрації при падінні OnWorkspaceCreated -> 500 з error_id
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"retry@example.com","password":"StrongPass123","name":"Retry"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on seed failure, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "error_id") {
		t.Errorf("expected error_id in 500 response, got %s", rec.Body.String())
	}

	// Лагодимо сід і пробуємо зареєструватися повторно з тим самим email -> має пройти
	mod.fail = false
	req2 := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"retry@example.com","password":"StrongPass123","name":"Retry"}`))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected 201 on retry registration after compensation, got %d (%s)", rec2.Code, rec2.Body.String())
	}
}

type failingCompensationStore struct {
	CoreStore
}

func (s *failingCompensationStore) DeleteUserAndWorkspace(_ context.Context, _, _ int64) error {
	return errors.New("compensation db delete failed")
}

func TestRegisterCompensationFailureLogsError(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())

	var stdoutBuf bytes.Buffer
	logger, err := logging.NewLogger(cfg, logging.WithLoggerStdout(&stdoutBuf))
	if err != nil {
		t.Fatalf("logging.NewLogger: %v", err)
	}
	defer logger.Close()

	reg := module.NewModuleRegistry()
	_ = reg.Register(&seedFailModule{name: "finance", fail: true})

	auth, err := NewAuthService(&failingCompensationStore{CoreStore: NewPGStore(pool)}, cfg, reg, logger)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	_, err = auth.Register(context.Background(), "compfail@example.com", "StrongPass123", "User", "WS")
	assertAppErrorCode(t, err, http.StatusInternalServerError, apperr.ErrInternal)

	logs := stdoutBuf.String()
	if !strings.Contains(logs, "register_compensation_failed") || !strings.Contains(logs, "error_id") {
		t.Fatalf("expected register_compensation_failed with error_id in logs, got:\n%s", logs)
	}
	if strings.Contains(logs, "compfail@example.com") {
		t.Fatalf("SECURITY VIOLATION: email leaked in compensation error log:\n%s", logs)
	}
}

func TestAuthLogsContainNoSecrets(t *testing.T) {
	pool, _ := testkit.OpenTestSchemaPool(t)
	cfg := newAuthTestConfig(t, t.TempDir())

	var stdoutBuf bytes.Buffer
	logger, err := logging.NewLogger(cfg, logging.WithLoggerStdout(&stdoutBuf))
	if err != nil {
		t.Fatalf("logging.NewLogger: %v", err)
	}
	defer logger.Close()

	auth, err := NewAuthService(NewPGStore(pool), cfg, module.NewModuleRegistry(), logger)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}

	ctx := context.Background()
	regRes, err := auth.Register(ctx, "secret-email@example.com", "MySecretPassword99!", "User", "WS")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	loginRes, err := auth.Login(ctx, "secret-email@example.com", "MySecretPassword99!", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	logs := stdoutBuf.String()
	for _, sensitive := range []string{"MySecretPassword99!", "secret-email@example.com", regRes.Token, loginRes.Token} {
		if strings.Contains(logs, sensitive) {
			t.Fatalf("SECURITY VIOLATION: sensitive value %q found in logs:\n%s", sensitive, logs)
		}
	}
}
